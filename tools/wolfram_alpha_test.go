package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func mockWolframServer(t *testing.T, response string) string {
	t.Helper()
	mockServer := http.NewServeMux()
	mockServer.HandleFunc("/v1/result", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(response))
	})

	server := httptest.NewServer(mockServer)
	t.Cleanup(server.Close)
	return server.URL + "/v1/result"
}

func TestWolframAlpha(t *testing.T) {
	testCases := []struct {
		name          string
		query         string
		mockResponse  string
		expectedError error
	}{
		{
			name:         "simple calculation",
			query:        "(1 + 2) * 3",
			mockResponse: "9",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			serviceURL := mockWolframServer(t, tc.mockResponse)
			wa := NewWolframAlphaWithServiceURL(serviceURL, "1234")
			_, err := wa.Execute(json.RawMessage(
				fmt.Sprintf(`{"query": %q}`, tc.query),
			))
			if tc.expectedError != nil {
				require.Error(t, err)
				require.EqualError(t, err, tc.expectedError.Error())
			} else {
				require.NoError(t, err)
			}
		})
	}
}
