package models_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/models"
)

func TestImageWireFormats(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				want := []byte("data:image/png;base64,YWJj")
				resp := `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`
				if provider == "anthropic" {
					want = []byte(`"data":"YWJj"`)
					resp = `{"stop_reason":"end_turn","content":[{"type":"text","text":"ok"}]}`
				}
				if !bytes.Contains(raw, want) || !bytes.Contains(raw, []byte("https://img.example/a.png")) {
					t.Errorf("missing image source in %s", raw)
				}
				_, _ = io.WriteString(w, resp)
			}))
			defer server.Close()
			var model h.Model = models.OpenAI{Model: "test", BaseURL: server.URL}
			if provider == "anthropic" {
				model = models.Anthropic{Model: "test", BaseURL: server.URL}
			}
			a := h.Agent[string]{Model: model, Output: h.Text()}
			out, err := a.Run(t.Context(), "inspect", h.Options{Images: []h.Image{{MediaType: "image/png", Data: []byte("abc")}, {URL: "https://img.example/a.png"}}})
			if err != nil || out.Output != "ok" {
				t.Fatal(out, err)
			}
		})
	}
}
