package capabilities_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	h "github.com/natexcvi/go-llm"
	c "github.com/natexcvi/go-llm/capabilities"
)

type guardedStream struct{ t *testing.T }

func (m guardedStream) Generate(context.Context, h.Request) (h.Response, error) {
	m.t.Fatal("expected stream")
	return h.Response{}, nil
}
func (m guardedStream) Stream(_ context.Context, q h.Request, emit func(string) error) (h.Response, error) {
	if strings.Contains(q.Messages[len(q.Messages)-1].Text, "example.org") {
		m.t.Fatal(q.Messages)
	}
	for _, s := range []string{"Contact user@", "example.org"} {
		if err := emit(s); err != nil {
			return h.Response{}, err
		}
	}
	return h.Response{Text: "Contact user@example.org"}, nil
}
func TestFiltersGuardStreamAndSnapshot(t *testing.T) {
	for _, tc := range []struct {
		f       c.TextFilter
		in, out string
	}{
		{c.RedactPersonalData, "Contact user@example.org", "Contact [redacted:email]"},
		{c.RedactPersonalData, "4111 1111 1111 1111", "[redacted:credit_card]"},
		{c.RedactPersonalData, "4111 1111 1111 1112", "4111 1111 1111 1112"},
		{c.RedactPersonalData, "GB82 WEST 1234 5698 7654 32", "[redacted:iban]"},
		{c.RedactSecrets, "sk-test00000000000000000000000000", "[redacted:openai_key]"},
	} {
		s, err := tc.f(tc.in)
		if err != nil || s != tc.out {
			t.Fatal(s, err)
		}
	}
	a := h.Agent[string]{Output: h.Text(), Model: guardedStream{t}, Capabilities: []h.Capability{c.FilterInput(c.RedactPersonalData), c.FilterOutput(c.RedactPersonalData)}}
	var observed string
	r, err := a.Run(t.Context(), "user@example.org", h.Options{Observe: func(_ context.Context, e h.Event) error {
		if strings.Contains(e.Text, "user@") {
			t.Fatal(e)
		}
		if e.Kind == "delta" {
			observed += e.Text
		}
		return nil
	}})
	b, _ := json.Marshal(r.Snapshot)
	if err != nil || r.Output != "Contact [redacted:email]" || observed != r.Output || strings.Contains(string(b), "example.org") {
		t.Fatal(r, err, observed)
	}
}
