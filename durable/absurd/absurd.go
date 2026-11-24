// Package absurd records agent steps within a registered Absurd task handler.
package absurd

import (
	"context"
	"errors"
	"fmt"
	"time"

	sdk "github.com/earendil-works/absurd/sdks/go/absurd"
	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/durable"
)

type Options struct {
	Authorize func(context.Context, h.Call) (h.Decision, error)
	// AwaitApproval enables durable event waits for deferred top-level calls.
	AwaitApproval bool
	Timeout       time.Duration
}

func ApprovalEvent(taskID string, step int, call h.Call) string {
	return fmt.Sprintf("agent.%s.%d.%s", taskID, step, call.ID)
}

// Run records each transition in the current Absurd task. The caller may emit
// "approve" or "deny" under ApprovalEvent in the same queue to resume a wait.
func Run[T any](ctx context.Context, a h.Agent[T], s h.Snapshot, opts Options) (h.Snapshot, error) {
	task, ok := sdk.TaskFromContext(ctx)
	if !ok {
		return s, errors.New("no running Absurd task")
	}
	in := durable.Input{Snapshot: s}
	for n := 0; !in.Snapshot.Complete; n++ {
		next, err := sdk.Step(ctx, fmt.Sprintf("agent-step-%d", n), func(ctx context.Context) (durable.StepResult, error) {
			return durable.Advance(ctx, a, in, opts.Authorize)
		})
		if err != nil {
			return in.Snapshot, err
		}
		in = durable.Input{Snapshot: next.Snapshot}
		if next.Paused {
			if !opts.AwaitApproval || next.Approval == nil {
				return in.Snapshot, h.ErrDeferred
			}
			in.Decision, err = sdk.AwaitEvent[string](ctx, ApprovalEvent(task.TaskID(), n, *next.Approval), sdk.AwaitEventOptions{Timeout: opts.Timeout})
			if err != nil {
				return in.Snapshot, err
			}
		}
	}
	return in.Snapshot, nil
}
