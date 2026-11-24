package harness_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/capabilities"
	"github.com/natexcvi/go-llm/storage"
)

func sequence(t *testing.T, steps ...func(h.Request) h.Response) h.Model {
	t.Helper()
	return h.ModelFunc(func(ctx context.Context, q h.Request) (h.Response, error) {
		t.Helper()
		if len(steps) == 0 {
			t.Fatal("unexpected model request")
		}
		step := steps[0]
		steps = steps[1:]
		return step(q), nil
	})
}
func call(name, args string) func(h.Request) h.Response {
	return func(h.Request) h.Response {
		return h.Response{Calls: []h.Call{{Name: name, Arguments: args, ID: "id-1"}}, InputTokens: 3, OutputTokens: 1}
	}
}
func TestToolValidationAndTypedOutput(t *testing.T) {
	type output struct {
		Quantity int `json:"quantity"`
	}
	invocations := 0
	tool := h.NewTool("sum", "Sum quantities", func(_ context.Context, in struct {
		Values []int `json:"values"`
	}) (int, error) {
		invocations++
		sum := 0
		for _, n := range in.Values {
			sum += n
		}
		return sum, nil
	})
	m := sequence(t, call("sum", `{}`), func(q h.Request) h.Response {
		if !strings.Contains(q.Messages[len(q.Messages)-1].Text, "values") {
			t.Fatal(q.Messages)
		}
		return call("sum", `{"values":[1,2]}`)(q)
	}, func(q h.Request) h.Response {
		if got := q.Messages[len(q.Messages)-1]; got.CallID != "id-1" || got.Text != "3" {
			t.Fatal(got)
		}
		return h.Response{Text: `{}`}
	}, func(q h.Request) h.Response {
		if !json.Valid(q.OutputSchema) || q.Messages[len(q.Messages)-1].Role != h.User {
			t.Fatal(q)
		}
		return h.Response{Text: `{"quantity":3}`}
	})
	a := h.Agent[output]{Model: m, Output: h.JSON[output](), Capabilities: []h.Capability{h.Tools(tool)}, MaxRetries: 2}
	history := []h.Message{{Role: h.User, Text: "old question"}, {Role: h.Assistant, Text: "old answer"}}
	res, err := a.Run(t.Context(), "add", h.Options{History: history})
	if err != nil {
		t.Fatal(err)
	}
	if res.Output.Quantity != 3 || invocations != 1 || res.Retries != 2 || res.Usage.Requests != 4 || res.Usage.ToolCalls != 2 || res.Usage.InputTokens != 6 {
		t.Fatal(res)
	}
	if len(history) != 2 || history[1].Text != "old answer" {
		t.Fatal(history)
	}
}
func TestBatchLimitsBeforeEffects(t *testing.T) {
	n := 0
	tool := h.NewTool("touch", "", func(context.Context, struct{}) (string, error) {
		n++
		return "", nil
	})
	a := h.Agent[string]{Model: h.ModelFunc(func(context.Context, h.Request) (h.Response, error) {
		return h.Response{Calls: []h.Call{{ID: "1", Name: "touch", Arguments: "{}"}, {ID: "2", Name: "touch", Arguments: "{}"}}}, nil
	}), Output: h.Text(), Capabilities: []h.Capability{h.Tools(tool)}, Limits: h.Limits{ToolCalls: 1}}
	_, err := a.Run(t.Context(), "", h.Options{})
	if !errors.Is(err, h.ErrLimit) || n != 0 {
		t.Fatalf("%v calls %d", err, n)
	}
}
func TestResumeRestoresPlanning(t *testing.T) {
	store, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	pause := errors.New("pause")
	a := h.Agent[string]{Model: sequence(t, call("write_plan", `{"items":[{"id":"build","content":"build"}]}`)), Output: h.Text(), Capabilities: []h.Capability{capabilities.Planning()}}
	_, err = a.Run(t.Context(), "make a plan", h.Options{Checkpoint: func(ctx context.Context, s h.Snapshot) error {
		if err := storage.Checkpoint(store, "runs/1")(ctx, s); err != nil {
			return err
		}
		return pause
	}})
	if !errors.Is(err, pause) {
		t.Fatal(err)
	}
	s, err := storage.Load(t.Context(), store, "runs/1")
	if err != nil {
		t.Fatal(err)
	}
	a.Model = sequence(t, call("read_plan", "{}"), func(q h.Request) h.Response {
		last := q.Messages[len(q.Messages)-1]
		if !strings.Contains(last.Text, "build") {
			t.Fatal(last)
		}
		return h.Response{Text: "ready"}
	})
	out, err := a.Resume(t.Context(), s, h.Options{})
	if err != nil || out.Output != "ready" || out.Usage.Requests != 3 {
		t.Fatal(out, err)
	}
}
func TestRunIsolationAndCleanup(t *testing.T) {
	a := h.Agent[string]{Model: h.ModelFunc(func(ctx context.Context, q h.Request) (h.Response, error) {
		if len(q.Messages) == 1 {
			return call("read_plan", "{}")(q), nil
		}
		return h.Response{Text: q.Messages[len(q.Messages)-1].Text}, nil
	}), Output: h.Text(), Capabilities: []h.Capability{capabilities.Planning()}}
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := a.Run(t.Context(), "read", h.Options{})
			if err != nil || res.Output != "[]" {
				t.Errorf("%+v %v", res, err)
			}
		}()
	}
	wg.Wait()
	cancelled := errors.New("observer stopped")
	var events []string
	done := false
	a.Capabilities = append(a.Capabilities, func(_ context.Context, r *h.Run) error {
		r.OnClose = append(r.OnClose, func() error {
			done = true
			return nil
		})
		return nil
	})
	_, err := a.Run(t.Context(), "", h.Options{Observe: func(_ context.Context, e h.Event) error {
		events = append(events, e.Kind)
		if e.Kind == "tool_call" {
			return cancelled
		}
		return nil
	}})
	if !done || !errors.Is(err, cancelled) || !slices.Equal(events, []string{"request", "response", "tool_call"}) {
		t.Fatal(err, done, events)
	}
}

func ExampleAgent() {
	type Answer struct {
		City string `json:"city"`
	}
	model := h.ModelFunc(func(context.Context, h.Request) (h.Response, error) {
		return h.Response{Text: `{"city":"Lisbon"}`}, nil
	})
	agent := h.Agent[Answer]{Model: model, Output: h.JSON[Answer]()}
	result, err := agent.Run(context.Background(), "What is the capital of Portugal?", h.Options{})
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Output.City)
	// Output: Lisbon
}

func TestApprovalResumeDoesNotRepeatPriorTool(t *testing.T) {
	n := 0
	tool := h.NewTool("increment", "", func(context.Context, struct{}) (int, error) {
		n++
		return n, nil
	})
	a := h.Agent[string]{Model: sequence(t, func(h.Request) h.Response {
		return h.Response{Calls: []h.Call{{Name: "increment", ID: "1", Arguments: "{}"}, {Name: "increment", ID: "2", Arguments: "{}"}}}
	}, func(q h.Request) h.Response { return h.Response{Text: q.Messages[len(q.Messages)-1].Text} }), Output: h.Text(), Capabilities: []h.Capability{h.Tools(tool)}}
	paused, err := a.Run(t.Context(), "run", h.Options{Authorize: func(_ context.Context, c h.Call) (h.Decision, error) {
		if c.ID == "2" {
			return h.Defer, nil
		}
		return h.Approve, nil
	}})
	if !errors.Is(err, h.ErrDeferred) || n != 1 || len(paused.Pending) != 1 {
		t.Fatal(n, paused, err)
	}
	again, err := a.Resume(t.Context(), paused.Snapshot, h.Options{})
	if !errors.Is(err, h.ErrDeferred) || n != 1 {
		t.Fatal(n, err)
	}
	out, err := a.Resume(t.Context(), again.Snapshot, h.Options{Authorize: func(context.Context, h.Call) (h.Decision, error) { return h.Approve, nil }})
	if err != nil || out.Output != "2" || n != 2 {
		t.Fatal(out, err)
	}
	replay, err := a.Resume(t.Context(), out.Snapshot, h.Options{})
	if err != nil || replay.Output != "2" || n != 2 {
		t.Fatal(replay, err)
	}
}

func TestBridgeAuthorizesUnderlyingTool(t *testing.T) {
	called := false
	tool := h.NewTool("edit", "", func(context.Context, struct{}) (string, error) {
		called = true
		return "", nil
	})
	a := h.Agent[string]{Output: h.Text(), Capabilities: []h.Capability{h.ToolBridge(h.Tools(tool))}, Model: sequence(t, func(q h.Request) h.Response {
		for _, d := range q.Tools {
			if d.Name == "edit" {
				t.Fatal("underlying tool exposed")
			}
		}
		return call("tool_call", `{"name":"edit","args_json":"{}"}`)(q)
	}, func(q h.Request) h.Response { return h.Response{Text: q.Messages[len(q.Messages)-1].Text} })}
	out, err := a.Run(t.Context(), "edit", h.Options{Authorize: func(ctx context.Context, c h.Call) (h.Decision, error) {
		if c.Name != "edit" || c.Arguments != "{}" {
			t.Fatal(c)
		}
		return h.Deny, nil
	}})
	if err != nil || called || !strings.Contains(out.Output, "denied") {
		t.Fatal(out, err)
	}
}
