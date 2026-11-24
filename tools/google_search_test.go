package tools

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func mockSearchService(t *testing.T) string {
	t.Helper()
	// Set up a mock server to respond to requests with a mock Google search results page
	mockServer := http.NewServeMux()
	mockServer.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		// Load the sample search results page from a local file
		file, err := ioutil.ReadFile("testdata/mock_search_results.html")
		if err != nil {
			http.Error(w, "Failed to load mock search results page", http.StatusInternalServerError)
			return
		}

		// Replace the search query in the mock search results page with the query from the request
		query := r.FormValue("q")
		fileString := string(file)
		fileString = strings.ReplaceAll(fileString, "{{QUERY}}", query)

		// Set the content type and write the mock search results page to the response
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(fileString))
	})

	server := httptest.NewServer(mockServer)
	t.Cleanup(server.Close)
	return server.URL
}

func TestWebSearch(t *testing.T) {
	testCases := []struct {
		name          string
		query         string
		expOutputPath string
	}{
		{
			name:          "simple",
			query:         "hello world",
			expOutputPath: "testdata/expected_parsed_results.json",
		},
	}
	serviceURL := mockSearchService(t)
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ws := NewWebSearch(serviceURL)
			output, err := ws.Execute(json.RawMessage(
				fmt.Sprintf(`{"query": %q}`, tc.query),
			))
			require.NoError(t, err)
			var results []SearchResult
			require.NoError(t, json.Unmarshal(output, &results))
			_, err = os.Stat(tc.expOutputPath)
			if os.IsNotExist(err) {
				require.NoError(t, ioutil.WriteFile(tc.expOutputPath, output, 0644))
			}
			expOutput, err := ioutil.ReadFile(tc.expOutputPath)
			require.NoError(t, err)
			var expResults []SearchResult
			require.NoError(t, json.Unmarshal(expOutput, &expResults))
			require.Equal(t, expResults, results)
		})
	}
}
