package capabilities_test

import (
	"context"
	"slices"
	"testing"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/capabilities"
)

// The expected triples (output, executed arguments, model requests) were
// independently obtained with FunctionModel on pydantic-ai-harness 0.73.0.
func TestUpstreamConformance(t *testing.T) {
	call := func(id, args string) h.Response {
		return h.Response{Calls: []h.Call{{ID: id, Name: "lookup", Arguments: args}}}
	}
	tests := []struct {
		name     string
		cap      h.Capability
		script   []h.Response
		executed []int
		output   string
	}{
		{"refinement", capabilities.RefineOutput(1, ""), []h.Response{{Text: "draft"}, {Text: "final"}}, nil, "final"},
		{"per-tool budget", capabilities.ToolBudgets(map[string]int{"lookup": 1}), []h.Response{call("a", `{"value":1}`), call("b", `{"value":2}`), {Text: "complete"}}, []int{1}, "complete"},
		{"argument repair", capabilities.RepairToolArguments(), []h.Response{call("a", `{'value':3,}`), {Text: "repaired"}}, []int{3}, "repaired"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var executed []int
			requests := 0
			tool := h.NewTool("lookup", "", func(_ context.Context, in struct {
				Value int `json:"value"`
			}) (int, error) {
				executed = append(executed, in.Value)
				return in.Value, nil
			})
			model := h.ModelFunc(func(context.Context, h.Request) (h.Response, error) {
				if requests >= len(tc.script) {
					t.Fatal("unexpected request")
				}
				response := tc.script[requests]
				requests++
				return response, nil
			})
			agent := h.Agent[string]{Model: model, Output: h.Text(), Capabilities: []h.Capability{tc.cap, h.Tools(tool)}}
			out, err := agent.Run(t.Context(), "fixture", h.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if out.Output != tc.output || out.Usage.Requests != len(tc.script) || !slices.Equal(executed, tc.executed) {
				t.Fatal(out, executed)
			}
		})
	}
}
