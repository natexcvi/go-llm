package tools

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/natexcvi/go-llm/engines"
	enginemocks "github.com/natexcvi/go-llm/engines/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type promptMatcher struct {
	expected string
}

func (m *promptMatcher) Matches(x interface{}) bool {
	prompt, ok := x.(*engines.ChatPrompt)
	if !ok {
		return false
	}
	for _, msg := range prompt.History {
		if strings.Contains(msg.Text, m.expected) {
			return true
		}
	}
	return false
}

func (m *promptMatcher) String() string {
	return "is a prompt with the expected text " +
		"in one of its messages"
}

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
			mockEngine.EXPECT().Chat(
				gomock.Matcher(&promptMatcher{expected: strippedPageContent}),
			).Return(&engines.ChatMessage{
				Role: engines.ConvRoleAssistant,
				Text: tc.expected,
			}, nil)
			ws := NewWebpageSummary(mockEngine)
			summary, err := ws.Execute([]byte(fmt.Sprintf(`{"url": %q}`, fmt.Sprintf(tc.url, srv.URL))))
			if tc.expError != nil {
				require.EqualError(t, err, tc.expError.Error())
				return
			}
			require.NoError(t, err)
			assert.JSONEq(t, fmt.Sprintf("%q", tc.expected), string(summary))
		})
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
