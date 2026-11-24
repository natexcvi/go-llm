// Package azure integrates the harness with the Microsoft Durable Task Go SDK.
// Register the activity and orchestrator on the worker's registry, then schedule
// the named orchestration with a harness.Start snapshot as its input.
package azure

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/microsoft/durabletask-go/task"
	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/durable"
)

type Options struct {
	Authorize func(context.Context, h.Call) (h.Decision, error)
	// ApprovalTimeout is the maximum time to wait for a decision. Zero
	// returns the paused snapshot to the caller. Negative waits indefinitely.
	ApprovalTimeout time.Duration
}

// ApprovalEvent identifies the event for an individual pending top-level call.
func ApprovalEvent(step int, call h.Call) string { return fmt.Sprintf("approve-%d-%s", step, call.ID) }

// Register installs a workflow named name and its name+".step" activity.
// The workflow returns a harness.Snapshot. Approvals use the JSON string
// "approve" or "deny" addressed to a specific orchestration instance.
func Register[T any](registry *task.TaskRegistry, name string, a h.Agent[T], opts Options) error {
	if registry == nil || name == "" {
		return errors.New("a task registry and orchestration name are required")
	}
	activityName := name + ".step"
	if err := registry.AddActivityN(activityName, func(ctx task.ActivityContext) (any, error) {
		var input durable.Input
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		return durable.Advance(ctx.Context(), a, input, opts.Authorize)
	}); err != nil {
		return err
	}
	return registry.AddOrchestratorN(name, func(ctx *task.OrchestrationContext) (any, error) {
		var input durable.Input
		if err := ctx.GetInput(&input.Snapshot); err != nil {
			return nil, err
		}
		for n := 0; !input.Snapshot.Complete; n++ {
			var next durable.StepResult
			if err := ctx.CallActivity(activityName, task.WithActivityInput(input)).Await(&next); err != nil {
				return nil, err
			}
			input = durable.Input{Snapshot: next.Snapshot}
			if next.Paused {
				if next.Approval == nil || opts.ApprovalTimeout == 0 {
					return next.Snapshot, nil
				}
				event := ApprovalEvent(n, *next.Approval)
				ctx.SetCustomStatus(event)
				if err := ctx.WaitForSingleEvent(event, opts.ApprovalTimeout).Await(&input.Decision); err != nil {
					return nil, err
				}
				ctx.SetCustomStatus("")
			}
		}
		return input.Snapshot, nil
	})
}
