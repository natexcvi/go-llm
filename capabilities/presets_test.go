package capabilities_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/capabilities"
	"github.com/natexcvi/go-llm/storage"
	"github.com/natexcvi/go-llm/workspace"
)

func TestCoderEditsAndReadsBack(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("Prefer small files."), 0600); err != nil {
		t.Fatal(err)
	}
	w, err := workspace.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	step := 0
	model := h.ModelFunc(func(ctx context.Context, q h.Request) (h.Response, error) {
		if !strings.Contains(q.Messages[0].Text, "Prefer small files.") {
			t.Fatal(q.Messages[0])
		}
		step++
		switch step {
		case 1:
			return h.Response{Calls: []h.Call{{Name: "write_file", ID: "a", Arguments: `{"path":"docs/hello","content":"hello"}`}, {Name: "edit_file", ID: "b", Arguments: `{"path":"docs/hello","old_text":"hello","new_text":"bye"}`}}}, nil
		case 2:
			return h.Response{Calls: []h.Call{{Name: "read_file", ID: "c", Arguments: `{"path":"docs/hello"}`}}}, nil
		default:
			return h.Response{Text: q.Messages[len(q.Messages)-1].Text}, nil
		}
	})
	agent := h.Agent[string]{Model: model, Output: h.Text(), Capabilities: []h.Capability{capabilities.Coder(w, w, "go")}}
	out, err := agent.Run(t.Context(), "edit", h.Options{})
	if err != nil || out.Output != "bye" {
		t.Fatal(out, err)
	}
}
func TestMemoryIsVisibleAcrossRuns(t *testing.T) {
	store, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a := h.Agent[string]{Output: h.Text(), Capabilities: []h.Capability{capabilities.Memory(store)}, Model: h.ModelFunc(func(ctx context.Context, q h.Request) (h.Response, error) {
		last := q.Messages[len(q.Messages)-1]
		if last.Role == h.ToolResult {
			return h.Response{Text: last.Text}, nil
		}
		c := h.Call{ID: "c", Name: "write_memory", Arguments: `{"file":"prefs","content":"green"}`}
		if last.Text == "recall" {
			if !strings.Contains(q.Messages[0].Text, "prefs") {
				t.Fatal(q.Messages)
			}
			c.Name = "read_memory"
			c.Arguments = `{"file":"prefs"}`
		}
		return h.Response{Calls: []h.Call{c}}, nil
	})}
	if _, err := a.Run(t.Context(), "save", h.Options{}); err != nil {
		t.Fatal(err)
	}
	out, err := a.Run(t.Context(), "recall", h.Options{})
	if err != nil || out.Output != "green" {
		t.Fatal(out, err)
	}
}
func TestLoopDetectionNormalizesArguments(t *testing.T) {
	n := 0
	a := h.Agent[string]{Output: h.Text(), Model: h.ModelFunc(func(context.Context, h.Request) (h.Response, error) {
		args := `{"x":1}`
		if n > 0 {
			args = `{ "x" : 1 }`
		}
		return h.Response{Calls: []h.Call{{ID: "x", Name: "repeat", Arguments: args}}}, nil
	}), Capabilities: []h.Capability{capabilities.DetectLoops(2), h.Tools(h.NewTool("repeat", "", func(_ context.Context, in map[string]json.RawMessage) (string, error) {
		n++
		return "again", nil
	}))}}
	_, err := a.Run(t.Context(), "repeat", h.Options{})
	if !errors.Is(err, capabilities.ErrLoop) || n != 1 {
		t.Fatal(n, err)
	}
}

func TestDelegationResumesWithoutReplayingChildTool(t *testing.T) {
	var performed []string
	childCalls := 0
	child := h.Agent[string]{Output: h.Text(), Model: h.ModelFunc(func(ctx context.Context, q h.Request) (h.Response, error) {
		childCalls++
		if childCalls == 1 {
			return h.Response{Calls: []h.Call{{Name: "record", ID: "first", Arguments: `{"value":"one"}`}, {Name: "record", ID: "second", Arguments: `{"value":"two"}`}}}, nil
		}
		return h.Response{Text: "done"}, nil
	}), Capabilities: []h.Capability{h.Tools(h.NewTool("record", "", func(ctx context.Context, in struct {
		Value string `json:"value"`
	}) (string, error) {
		performed = append(performed, in.Value)
		return in.Value, nil
	}))}}
	parent := h.Agent[string]{Output: h.Text(), Capabilities: []h.Capability{capabilities.SubAgent("delegate", "", child)}, Model: h.ModelFunc(func(ctx context.Context, q h.Request) (h.Response, error) {
		last := q.Messages[len(q.Messages)-1]
		if last.Role == h.ToolResult {
			return h.Response{Text: last.Text}, nil
		}
		return h.Response{Calls: []h.Call{{Name: "delegate", ID: "parent", Arguments: `{"task":"do both"}`}}}, nil
	})}
	pause, err := parent.Run(t.Context(), "begin", h.Options{Authorize: func(_ context.Context, c h.Call) (h.Decision, error) {
		if c.ID == "second" {
			return h.Defer, nil
		}
		return h.Approve, nil
	}})
	if !errors.Is(err, h.ErrDeferred) || strings.Join(performed, ",") != "one" {
		t.Fatal(performed, err)
	}
	b, err := json.Marshal(pause.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var saved h.Snapshot
	if err = json.Unmarshal(b, &saved); err != nil {
		t.Fatal(err)
	}
	result, err := parent.Resume(t.Context(), saved, h.Options{Authorize: func(context.Context, h.Call) (h.Decision, error) { return h.Approve, nil }})
	if err != nil || result.Output != "done" || strings.Join(performed, ",") != "one,two" || childCalls != 2 {
		t.Fatal(result, performed, err)
	}
}
func TestSkillsCatalogAndLoad(t *testing.T) {
	files := fstest.MapFS{
		"lib/draft/SKILL.md":   {Data: []byte("---\nname: draft\ndescription: Draft a note.\nwhen_to_use: For a new report.\n---\n\nUse ${SKILL_DIR}/template.\n")},
		"lib/private/SKILL.md": {Data: []byte("---\ndescription: Hidden.\ndisable-model-invocation: true\n---\nrestricted")},
	}
	a := h.Agent[string]{Output: h.Text(), Capabilities: []h.Capability{capabilities.Skills(files, "lib")}, Model: h.ModelFunc(func(ctx context.Context, q h.Request) (h.Response, error) {
		catalog := q.Messages[0].Text
		if !strings.Contains(catalog, "For a new report.") || strings.Contains(catalog, "Hidden") || strings.Contains(catalog, "template") {
			t.Fatal(catalog)
		}
		last := q.Messages[len(q.Messages)-1]
		if last.Role == h.ToolResult {
			return h.Response{Text: last.Text}, nil
		}
		return h.Response{Calls: []h.Call{{ID: "load", Name: "load_capability", Arguments: `{"name":"draft"}`}}}, nil
	})}
	out, err := a.Run(t.Context(), "use skill", h.Options{})
	if err != nil || !strings.Contains(out.Output, "Use lib/draft/template.") || strings.Contains(out.Output, "when_to_use") {
		t.Fatal(out, err)
	}
}
