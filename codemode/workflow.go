package codemode

import (
	"context"
	"errors"
	"fmt"
	"slices"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/capabilities"
)

// WorkflowAgent exposes a child as a named function that takes a task string.
type WorkflowAgent struct {
	Name, Description string
	Agent             h.Agent[string]
}
type workflowKey struct{}

// DynamicWorkflow registers run_workflow, a program that can delegate,
// aggregate and transform results from a fixed catalog of child agents.
// MaxCalls counts completed or failed child calls across the entire parent run.
// The child uses its own model and limits. A nil child model inherits the parent's.
func (e *Engine) DynamicWorkflow(maxCalls int, agents ...WorkflowAgent) h.Capability {
	agents = slices.Clone(agents)
	return func(ctx context.Context, r *h.Run) error {
		if ctx.Value(workflowKey{}) != nil {
			return nil
		}
		if maxCalls < 1 || len(agents) == 0 {
			return errors.New("workflow agents and positive call limit required")
		}
		var used int
		if err := h.BindState(r, "workflow.calls", &used); err != nil {
			return err
		}
		var tools []h.Tool
		for _, entry := range agents {
			child := entry.Agent
			if child.Model == nil {
				child.Model = r.Model
			}
			if child.Output.Decode == nil {
				child.Output = h.Text()
			}
			t, err := capabilities.DelegationTool(r, entry.Name, entry.Description, child)
			if err != nil {
				return err
			}
			call := t.Execute
			t.Execute = func(ctx context.Context, c h.Call) (string, error) {
				if used >= maxCalls {
					return "", fmt.Errorf("%w: workflow agent calls", h.ErrLimit)
				}
				result, err := call(context.WithValue(ctx, workflowKey{}, true), c)
				if !errors.Is(err, h.ErrDeferred) {
					used++
				}
				return result, err
			}
			tools = append(tools, t)
		}
		return e.programCapability("run_workflow", tools, false)(ctx, r)
	}
}
