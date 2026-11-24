package codemode_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/capabilities"
	"github.com/natexcvi/go-llm/codemode"
	"github.com/natexcvi/go-llm/storage"
)

func buildEngine(t *testing.T) *codemode.Engine {
	t.Helper()
	path := filepath.Join(t.TempDir(), "guest.wasm")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", path, "./cmd/harness-code")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=wasip1", "GOARCH=wasm")
	if log, err := build.CombinedOutput(); err != nil {
		t.Fatal(string(log), err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := codemode.Open(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close(context.Background()) })
	return engine
}

func TestProgramResumesAtNestedApproval(t *testing.T) {
	engine := buildEngine(t)
	var called []int
	type args struct {
		X int `json:"x"`
	}
	tool := h.NewTool("double", "Double a number.", func(_ context.Context, a args) (int, error) {
		called = append(called, a.X)
		return 2 * a.X, nil
	})
	requests := 0
	script := "vals = [double(x=i) for i in [2, 3]]\n[v+1 for v in vals]"
	b, err := json.Marshal(map[string]string{"code": script})
	if err != nil {
		t.Fatal(err)
	}
	model := h.ModelFunc(func(ctx context.Context, q h.Request) (h.Response, error) {
		requests++
		if len(q.Tools) != 1 || q.Tools[0].Name != "run_code" {
			t.Fatal(q.Tools)
		}
		if last := q.Messages[len(q.Messages)-1]; last.Role == h.ToolResult {
			return h.Response{Text: last.Text}, nil
		}
		return h.Response{Calls: []h.Call{{ID: "program", Name: "run_code", Arguments: string(b)}}}, nil
	})
	a := h.Agent[string]{Model: model, Output: h.Text(), Capabilities: []h.Capability{engine.Capability(tool), capabilities.ToolBudgets(map[string]int{"run_code": 1}, "run_code")}, Limits: h.Limits{ToolCalls: 3}}
	paused, err := a.Run(t.Context(), "compute", h.Options{Authorize: func(_ context.Context, c h.Call) (h.Decision, error) {
		if c.Name == "double" && c.ID == "program/2" {
			return h.Defer, nil
		}
		return h.Approve, nil
	}})
	if !errors.Is(err, h.ErrDeferred) || !slices.Equal(called, []int{2}) {
		t.Fatal(paused, called, err)
	}
	raw, err := json.Marshal(paused.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var saved h.Snapshot
	if err = json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	done, err := a.Resume(t.Context(), saved, h.Options{Authorize: func(context.Context, h.Call) (h.Decision, error) { return h.Approve, nil }})
	if err != nil || done.Output != "[5,7]" || !slices.Equal(called, []int{2, 3}) || requests != 2 || done.Usage.ToolCalls != 3 {
		t.Fatal(done, called, requests, err)
	}
}

func TestCapabilityCreationActivatesNextRun(t *testing.T) {
	engine := buildEngine(t)
	dir, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	model := h.ModelFunc(func(ctx context.Context, q h.Request) (h.Response, error) {
		last := q.Messages[len(q.Messages)-1]
		if last.Role == h.ToolResult {
			return h.Response{Text: last.Text}, nil
		}
		exists := slices.ContainsFunc(q.Tools, func(d h.Definition) bool { return d.Name == "authored_total" })
		var c h.Call
		switch last.Text {
		case "create":
			if exists {
				t.Fatal("capability active before creation")
			}
			c = h.Call{ID: "new", Name: "author_capability", Arguments: `{"name":"total","code":"params['x'] + params['y']"}`}
		case "invoke":
			if !exists {
				t.Fatal("missing authored capability")
			}
			c = h.Call{ID: "call", Name: "authored_total", Arguments: `{"x":2,"y":3}`}
		case "disable":
			c = h.Call{ID: "off", Name: "disable_authored_capability", Arguments: `{"name":"total"}`}
		case "inspect":
			if exists {
				t.Fatal("disabled capability still present")
			}
			return h.Response{Text: "inactive"}, nil
		}
		return h.Response{Calls: []h.Call{c}}, nil
	})
	a := h.Agent[string]{Model: model, Output: h.Text(), Capabilities: []h.Capability{engine.CapabilityCreation(dir), engine.LoadActive(dir)}}
	for _, tc := range []struct{ prompt, want string }{{"create", "Saved authored_total for the next run."}, {"invoke", "5"}, {"disable", "total"}, {"inspect", "inactive"}} {
		result, err := a.Run(t.Context(), tc.prompt, h.Options{})
		if err != nil || result.Output != tc.want {
			t.Fatal(tc.prompt, result.Output, err)
		}
	}
}

func TestDynamicWorkflowCallsChildrenWithinSharedBudget(t *testing.T) {
	engine := buildEngine(t)
	childCalls := 0
	child := h.ModelFunc(func(_ context.Context, q h.Request) (h.Response, error) {
		childCalls++
		if len(q.Messages) != 1 {
			t.Fatal(q.Messages)
		}
		return h.Response{Text: q.Messages[0].Text + "!"}, nil
	})
	use := func(limit int) (h.Result[string], error) {
		cap := engine.DynamicWorkflow(limit, codemode.WorkflowAgent{Name: "draft", Agent: h.Agent[string]{Model: child}})
		a := h.Agent[string]{Output: h.Text(), Capabilities: []h.Capability{cap}, Model: h.ModelFunc(func(ctx context.Context, q h.Request) (h.Response, error) {
			if len(q.Tools) != 1 || q.Tools[0].Name != "run_workflow" {
				t.Fatal(q.Tools)
			}
			last := q.Messages[len(q.Messages)-1]
			if last.Role == h.ToolResult {
				return h.Response{Text: last.Text}, nil
			}
			return h.Response{Calls: []h.Call{{Name: "run_workflow", ID: "wf", Arguments: `{"code":"[draft(task=x) for x in ['a','b']]"}`}}}, nil
		})}
		return a.Run(t.Context(), "compose", h.Options{})
	}
	result, err := use(2)
	if err != nil || result.Output != `["a!","b!"]` || childCalls != 2 {
		t.Fatal(result, childCalls, err)
	}
	_, err = use(1)
	if !errors.Is(err, h.ErrLimit) || childCalls != 3 {
		t.Fatal(childCalls, err)
	}
}

func TestREPLRetainsFunctionsWithoutRepeatingEffects(t *testing.T) {
	engine := buildEngine(t)
	effects := 0
	tool := h.NewTool("next", "", func(context.Context, struct{}) (int, error) { effects++; return 11, nil })
	scripts := []string{"x = next()\ndef twice(v):\n    return 2*v\nprint('first')\nx", "twice(x)", "x = 3\nx", "x+1"}
	expected := []string{"first\n11", "22", "3", "4"}
	n := 0
	a := h.Agent[string]{Output: h.Text(), Capabilities: []h.Capability{engine.Capability(tool)}, Model: h.ModelFunc(func(_ context.Context, q h.Request) (h.Response, error) {
		if n > 0 && q.Messages[len(q.Messages)-1].Text != expected[n-1] {
			t.Fatal(q.Messages)
		}
		if n == len(scripts) {
			return h.Response{Text: "ok"}, nil
		}
		b, _ := json.Marshal(map[string]any{"code": scripts[n], "restart": n == 2})
		n++
		return h.Response{Calls: []h.Call{{ID: "snippet", Name: "run_code", Arguments: string(b)}}}, nil
	})}
	s := h.Start("compute", nil)
	for i := 0; i < 10 && !s.Complete; i++ {
		result, err := a.Advance(t.Context(), s, h.Options{})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(result.Snapshot)
		s = h.Snapshot{}
		if err := json.Unmarshal(b, &s); err != nil {
			t.Fatal(err)
		}
	}
	if effects != 1 || !s.Complete {
		t.Fatal(effects, s)
	}
}
