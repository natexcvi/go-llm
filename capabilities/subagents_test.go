package capabilities_test

import (
	"context"
	"errors"
	"testing"

	h "github.com/natexcvi/go-llm"
	c "github.com/natexcvi/go-llm/capabilities"
)

func TestRosterSharesUsageAcrossDeferredChild(t *testing.T) {
	childCalls, processed, events := 0, 0, 0
	model := h.ModelFunc(func(context.Context, h.Request) (h.Response, error) {
		childCalls++
		if childCalls == 1 {
			return h.Response{InputTokens: 11, Calls: []h.Call{{Name: "effect", ID: "approved", Arguments: "{}"}, {Name: "effect", ID: "wait", Arguments: "{}"}}}, nil
		}
		return h.Response{Text: "child answer", InputTokens: 13}, nil
	})
	child := h.Agent[string]{Capabilities: []h.Capability{h.Tools(h.NewTool("effect", "", func(context.Context, struct{}) (string, error) { processed++; return "ok", nil }))}}
	cfg := c.SubAgents{Agents: []c.Delegate{{Name: "worker", Agent: child, Models: []string{"fast"}, MaxCalls: 1}}, Models: map[string]h.Model{"fast": model}, ForwardEvents: true}
	parentCalls := 0
	a := h.Agent[string]{Output: h.Text(), Limits: h.Limits{Requests: 4, ToolCalls: 3}, Capabilities: []h.Capability{cfg.Capability()}, Model: h.ModelFunc(func(_ context.Context, q h.Request) (h.Response, error) {
		parentCalls++
		if parentCalls == 1 {
			return h.Response{InputTokens: 5, Calls: []h.Call{{Name: "delegate_task", ID: "d", Arguments: `{"task":"gather","agent_name":"worker"}`}}}, nil
		}
		if q.Messages[len(q.Messages)-1].Text != "child answer" {
			t.Fatal(q.Messages)
		}
		return h.Response{Text: "done", InputTokens: 7}, nil
	})}
	allow := false
	opts := h.Options{Authorize: func(_ context.Context, call h.Call) (h.Decision, error) {
		if call.ID == "wait" && !allow {
			return h.Defer, nil
		}
		return h.Approve, nil
	}, Observe: func(_ context.Context, e h.Event) error {
		if len(e.Delegates) > 0 && e.Delegates[0] == "worker" && e.Kind == "request" {
			events++
		}
		return nil
	}}
	r, err := a.Run(t.Context(), "start", opts)
	if !errors.Is(err, h.ErrDeferred) || r.Usage.Requests != 2 || r.Usage.ToolCalls != 1 || processed != 1 {
		t.Fatal(r, err)
	}
	allow = true
	r, err = a.Resume(t.Context(), r.Snapshot, opts)
	if err != nil || r.Usage.Requests != 4 || r.Usage.ToolCalls != 3 || r.Usage.InputTokens != 36 || childCalls != 2 || processed != 2 || events != 2 {
		t.Fatal(r, err, childCalls, processed, events)
	}
	parentCalls, childCalls, processed = 0, 0, 0
	a.Limits.ToolCalls = 2
	r, err = a.Run(t.Context(), "start", opts)
	if !errors.Is(err, h.ErrLimit) || processed != 0 || r.Usage.Requests != 2 {
		t.Fatal(r, err, processed)
	}
}

func TestSelfDelegateGetsFreshHistoryAndOriginalTools(t *testing.T) {
	seen := 0
	a := h.Agent[string]{Output: h.Text(), Capabilities: []h.Capability{(c.SubAgents{IncludeSelf: true, MaxDepth: 2}).Capability(), h.Tools(h.NewTool("effect", "", func(context.Context, struct{}) (string, error) { seen++; return "effect", nil }))}, Model: h.ModelFunc(func(_ context.Context, q h.Request) (h.Response, error) {
		last := q.Messages[len(q.Messages)-1]
		switch last.Text {
		case "parent":
			return h.Response{Calls: []h.Call{{ID: "d", Name: "delegate_task", Arguments: `{"agent_name":"self","task":"child"}`}}}, nil
		case "child":
			for _, m := range q.Messages {
				if m.Text == "parent" {
					t.Fatal("leaked parent history")
				}
			}
			return h.Response{Calls: []h.Call{{ID: "nested", Name: "effect", Arguments: "{}"}}}, nil
		default:
			return h.Response{Text: last.Text}, nil
		}
	})}
	r, err := a.Run(t.Context(), "parent", h.Options{})
	if err != nil || r.Output != "effect" || seen != 1 || r.Usage.Requests != 4 || r.Usage.ToolCalls != 2 {
		t.Fatal(r, err)
	}
}
