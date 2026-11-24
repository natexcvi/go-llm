package integrations_test

import (
	"context"
	"testing"
	"testing/fstest"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/integrations"
)

func TestDocsExpandsAliasesAndCachesForOneRun(t *testing.T) {
	files := fstest.MapFS{"tools.md": {Data: []byte("Use {{ openai:medium.name }}.")}, "model_aliases/aliases.json": {Data: []byte(`{"openai:medium":"openai:example"}`)}}
	calls := 0
	a := h.Agent[string]{Output: h.Text(), Capabilities: []h.Capability{integrations.PydanticAIDocs{Local: files}.Capability()}, Model: h.ModelFunc(func(ctx context.Context, q h.Request) (h.Response, error) {
		calls++
		if calls == 2 {
			files["tools.md"] = &fstest.MapFile{Data: []byte("changed")}
		}
		if calls <= 2 {
			return h.Response{Calls: []h.Call{{Name: "read_pyai_docs", ID: "doc", Arguments: `{"topic":"tools"}`}}}, nil
		}
		return h.Response{Text: q.Messages[len(q.Messages)-1].Text}, nil
	})}
	out, err := a.Run(t.Context(), "tools", h.Options{})
	if err != nil || out.Output != "Use example." {
		t.Fatal(out.Output, err)
	}
}
