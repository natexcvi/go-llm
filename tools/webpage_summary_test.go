package tools

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/golang/mock/gomock"
	"github.com/natexcvi/go-llm/engines"
	enginemocks "github.com/natexcvi/go-llm/engines/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readTestFile(t *testing.T, filePath string) []byte {
	t.Helper()
	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	return content
}

func mockWebServer(t *testing.T, content []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := w.Write(content)
		require.NoError(t, err)
	}))
	t.Cleanup(func() {
		srv.Close()
	})
	return srv
}

func TestWebpageSumamry(t *testing.T) {
	testCases := []struct {
		name            string
		url             string
		pageContentPath string
		expected        string
		expError        error
	}{
		{
			name:            "Wikipedia",
			url:             "%s/wiki/Go_(programming_language)",
			pageContentPath: "testdata/golang_wikipedia_article.html",
			expected: "Go is a statically typed, compiled programming language designed " +
				"at Google by Robert Griesemer, Rob Pike, and Ken Thompson. Go is " +
				"syntactically similar to C, but with memory safety, garbage " +
				"collection, structural typing, and CSP-style concurrency. " +
				"The compiler, tools, and source code are all free and open source. " +
				"Go was originally developed at Google in 2007 by Robert Griesemer, " +
				"Rob Pike, and Ken Thompson. It is used by Google, Cloudflare, " +
				"Dropbox, Facebook, IBM, Intel, Netflix, Pinterest, SoundCloud, " +
				"Spotify, and many other companies. It is also the language of " +
				"choice for many infrastructure tools, such as Docker, " +
				"Kubernetes, and Terraform. Go was announced in November " +
				"2009. The language is often referred to as Golang because " +
				"of its domain name, golang.org, but the proper name is Go.",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockEngine := enginemocks.NewMockLLM(ctrl)
			srv := mockWebServer(t, readTestFile(t, tc.pageContentPath))
			var prompts []string
			var requestSizes []int
			mockEngine.EXPECT().Chat(gomock.Any()).DoAndReturn(func(prompt *engines.ChatPrompt) (*engines.ChatMessage, error) {
				prompts = append(prompts, prompt.History[len(prompt.History)-1].Text)
				requestSizes = append(requestSizes, promptBytes(prompt))
				return &engines.ChatMessage{
					Role: engines.ConvRoleAssistant,
					Text: tc.expected,
				}, nil
			}).AnyTimes()
			ws := NewWebpageSummary(mockEngine)
			summary, err := ws.Execute([]byte(fmt.Sprintf(`{"url": %q}`, fmt.Sprintf(tc.url, srv.URL))))
			if tc.expError != nil {
				require.EqualError(t, err, tc.expError.Error())
				return
			}
			require.NoError(t, err)
			assert.JSONEq(t, fmt.Sprintf("%q", tc.expected), string(summary))
			require.Greater(t, len(prompts), 1, "a long article should be split into chunks")
			for _, size := range requestSizes {
				assert.LessOrEqual(t, size, defaultContextBudgetBytes-defaultResponseReserveBytes)
			}
		})
	}
}

// A page longer than one chunk is summarised chunk by chunk, each call carrying the running summary so the whole
// prompt never exceeds a safe context size (issue #6).
func TestWebpageSummaryChunksWithCumulativeSummary(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockEngine := enginemocks.NewMockLLM(ctrl)
	page := "<html><body><p>chunk one alpha beta " + strings.Repeat("filler ", 24) + "</p><p>chunk two gamma delta</p></body></html>"
	srv := mockWebServer(t, []byte(page))

	var prompts []string
	mockEngine.EXPECT().Chat(gomock.Any()).DoAndReturn(func(prompt *engines.ChatPrompt) (*engines.ChatMessage, error) {
		text := prompt.History[len(prompt.History)-1].Text
		prompts = append(prompts, text)
		return &engines.ChatMessage{Role: engines.ConvRoleAssistant, Text: fmt.Sprintf("summary %d", len(prompts))}, nil
	}).AnyTimes()

	ws := NewWebpageSummaryWithBudget(mockEngine, 300, 50)
	result, err := ws.Execute([]byte(fmt.Sprintf(`{"url": %q}`, srv.URL)))
	require.NoError(t, err)
	assert.JSONEq(t, fmt.Sprintf("%q", fmt.Sprintf("summary %d", len(prompts))), string(result))
	require.GreaterOrEqual(t, len(prompts), 2, "the long page should be summarised in chunks")
	assert.Contains(t, prompts[0], "chunk one alpha")
	assert.NotContains(t, prompts[0], "chunk two gamma delta")
	assert.Contains(t, prompts[1], "summary 1", "the second chunk must see the cumulative summary")
	assert.Contains(t, prompts[len(prompts)-1], "delta", "the last chunk must see its own content")
}

func TestWebpageSummaryBoundsEveryRequestWithMultibyteSpanAndLongSummary(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockEngine := enginemocks.NewMockLLM(ctrl)
	span := strings.Repeat("😀", 80)
	srv := mockWebServer(t, []byte("<html><body><p>"+span+"</p></body></html>"))
	longSummary := strings.Repeat("summary-😀", 24)

	var prompts []string
	var requestSizes []int
	mockEngine.EXPECT().Chat(gomock.Any()).DoAndReturn(func(prompt *engines.ChatPrompt) (*engines.ChatMessage, error) {
		prompts = append(prompts, prompt.History[len(prompt.History)-1].Text)
		requestSizes = append(requestSizes, promptBytes(prompt))
		return &engines.ChatMessage{Role: engines.ConvRoleAssistant, Text: longSummary}, nil
	}).AnyTimes()

	const contextBudget = 300
	const responseReserve = 50
	ws := NewWebpageSummaryWithBudget(mockEngine, contextBudget, responseReserve)
	_, err := ws.Execute([]byte(fmt.Sprintf(`{"url": %q}`, srv.URL)))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(requestSizes), 2, "the unbroken span should be split into multiple requests")
	for index, size := range requestSizes {
		assert.LessOrEqual(t, size, contextBudget-responseReserve, "request %d exceeds the input budget", index+1)
		assert.True(t, utf8.ValidString(prompts[index]), "request %d splits a UTF-8 sequence", index+1)
	}
	require.GreaterOrEqual(t, len(prompts), 2)
	assert.NotContains(t, prompts[1], longSummary, "a long previous summary must be capped before reuse")
}

func TestWebpageSummaryErrorsWhenFixedInstructionsCannotFit(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockEngine := enginemocks.NewMockLLM(ctrl)
	srv := mockWebServer(t, []byte("<html><body><p>content</p></body></html>"))

	ws := NewWebpageSummaryWithBudget(mockEngine, 80, 20)
	_, err := ws.Execute([]byte(fmt.Sprintf(`{"url": %q}`, srv.URL)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "webpage summary fixed instructions cannot fit")
}

func TestSplitIntoChunks(t *testing.T) {
	chunks := splitIntoChunks("one two three four five six seven", 12)
	assert.Equal(t, []string{"one two", "three four", "five six", "seven"}, chunks)
	assert.Empty(t, splitIntoChunks("", 12))
	assert.Equal(t, []string{"a", "long", "word"}, splitIntoChunks("a longword", 4), "long words are split on byte boundaries")

	multibyteChunks := splitIntoChunks(strings.Repeat("😀", 5), 8)
	assert.Equal(t, []string{"😀😀", "😀😀", "😀"}, multibyteChunks)
	for _, chunk := range multibyteChunks {
		assert.LessOrEqual(t, len(chunk), 8)
		assert.True(t, utf8.ValidString(chunk))
	}
}

func TestStripHTMLTags(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple",
			input:    "hello, <b>world</b>",
			expected: "hello, world",
		},
		{
			name:     "with newlines",
			input:    "hello,\n\n<b>world</b>",
			expected: "hello, world",
		},
		{
			name:     "with nested tags",
			input:    "hello, <b>world <i>and</i> universe</b>",
			expected: "hello, world and universe",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, (&WebpageSummary{}).stripHTMLTags(tc.input))
		})
	}
}
