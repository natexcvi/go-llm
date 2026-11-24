// Package lambda runs a Go agent inside an AWS Lambda durable function.
// Each model request and each top-level tool call is a separate recorded step.
// Keep the agent configuration and step sequence stable across replays. The
// application provides credentials, its approval policy and the Lambda handler.
package lambda

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-durable-execution-sdk-go/durable"
	h "github.com/natexcvi/go-llm"
	activity "github.com/natexcvi/go-llm/durable"
)

// Options applies application policy during each activity. When a tool is
// deferred, Submit delivers the provider-issued callback ID to the application.
// Resolve the callback with the JSON string "approve" or "deny".
type Options struct {
	Authorize func(context.Context, h.Call) (h.Decision, error)
	Submit    func(context.Context, h.Call, string) error
}

// Run returns a completed snapshot or a snapshot paused for authorization if
// no Submit callback is configured. Pass a saved snapshot to continue a run.
func Run[T any](ctx durable.Context, a h.Agent[T], s h.Snapshot, opts Options) (h.Result[T], error) {
	var reply string
	for n := 0; !s.Complete; n++ {
		input := activity.Input{Snapshot: s, Decision: reply}
		next, err := durable.Step(ctx, fmt.Sprintf("agent-step-%d", n), func(ctx durable.StepContext) (activity.StepResult, error) {
			return activity.Advance(ctx, a, input, opts.Authorize)
		})
		if err != nil {
			return h.Result[T]{Snapshot: s}, err
		}
		s = next.Snapshot
		reply = ""
		if !next.Paused {
			continue
		}
		if opts.Submit == nil || next.Approval == nil {
			return h.Result[T]{Snapshot: s}, h.ErrDeferred
		}
		target := *next.Approval
		reply, err = durable.WaitForCallback[string](ctx, fmt.Sprintf("agent-approval-%d", n), func(ctx durable.StepContext, id string) error { return opts.Submit(ctx, target, id) }, durable.WithCallbackSerdes(durable.JSONSerdes))
		if err != nil {
			return h.Result[T]{Snapshot: s}, err
		}
		if reply != "approve" && reply != "deny" {
			return h.Result[T]{Snapshot: s}, errors.New("invalid approval callback result")
		}
	}
	// The final step produced validated output. Decode the wire copy after replay.
	if a.Output.Decode == nil || len(s.History) == 0 || s.History[len(s.History)-1].Role != h.Assistant {
		return h.Result[T]{Snapshot: s}, errors.New("invalid completed agent state")
	}
	value, err := a.Output.Decode(s.History[len(s.History)-1].Text)
	return h.Result[T]{Snapshot: s, Output: value}, err
}
