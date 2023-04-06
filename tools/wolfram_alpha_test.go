package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func mockWolframServer(t *testing.T, response string) {
	t.Helper()
	// Set up a mock server to respond to requests with a mock Google search results page
	mockServer := http.NewServeMux()
	mockServer.HandleFunc("/v1/result", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(response))
	})

	server := &http.Server{
		Addr:    ":8080",
		Handler: mockServer,
	}

	// Start the mock server on port 8080
	go func() {
		if err := server.ListenAndServe(); err != nil {
			if err == http.ErrServerClosed {
				return
			}
			log.Fatal(err)
		}
	}()
	t.Cleanup(func() {
		require.NoError(t, server.Shutdown(context.Background()))
	})
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
			mockWolframServer(t, tc.mockResponse)
			wa := NewWolframAlphaWithServiceURL("http://localhost:8080/v1/result", "1234")
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
