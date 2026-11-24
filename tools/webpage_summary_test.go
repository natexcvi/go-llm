package tools

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

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
			strippedPageContent := (&WebpageSummary{}).stripHTMLTags(
				string(readTestFile(t, tc.pageContentPath)),
			)
			var prompts []string
			mockEngine.EXPECT().Chat(gomock.Any()).DoAndReturn(func(prompt *engines.ChatPrompt) (*engines.ChatMessage, error) {
				prompts = append(prompts, prompt.History[len(prompt.History)-1].Text)
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
			chunks := splitIntoChunks(strippedPageContent, defaultChunkSize)
			require.Greater(t, len(chunks), 1, "a long article should be split into chunks")
			require.Len(t, prompts, len(chunks))
			for index, chunk := range chunks {
				assert.Contains(t, prompts[index], chunk)
			}
		})
	}
}

// A page longer than one chunk is summarised chunk by chunk, each call carrying the running summary so the whole
// prompt never exceeds a safe context size (issue #6).
func TestWebpageSummaryChunksWithCumulativeSummary(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockEngine := enginemocks.NewMockLLM(ctrl)
	srv := mockWebServer(t, []byte("<html><body><p>chunk one alpha beta</p><p>chunk two gamma delta</p></body></html>"))

	var prompts []string
	mockEngine.EXPECT().Chat(gomock.Any()).DoAndReturn(func(prompt *engines.ChatPrompt) (*engines.ChatMessage, error) {
		text := prompt.History[len(prompt.History)-1].Text
		prompts = append(prompts, text)
		return &engines.ChatMessage{Role: engines.ConvRoleAssistant, Text: fmt.Sprintf("summary %d", len(prompts))}, nil
	}).Times(2)

	ws := NewWebpageSummaryWithChunkSize(mockEngine, 40)
	result, err := ws.Execute([]byte(fmt.Sprintf(`{"url": %q}`, srv.URL)))
	require.NoError(t, err)
	assert.JSONEq(t, `"summary 2"`, string(result))
	require.Len(t, prompts, 2, "the long page should be summarised in two chunks")
	assert.Contains(t, prompts[0], "chunk one alpha")
	assert.NotContains(t, prompts[0], "chunk two gamma delta")
	assert.Contains(t, prompts[1], "summary 1", "the second chunk must see the cumulative summary")
	assert.Contains(t, prompts[1], "delta", "the second chunk must see its own content")
}

func TestSplitIntoChunks(t *testing.T) {
	chunks := splitIntoChunks("one two three four five six seven", 12)
	assert.Equal(t, []string{"one two", "three four", "five six", "seven"}, chunks)
	assert.Empty(t, splitIntoChunks("", 12))
	assert.Equal(t, []string{"a", "longword"}, splitIntoChunks("a longword", 4), "words longer than the chunk size are kept whole")
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
