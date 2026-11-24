// Package durable defines the serializable activity boundary used by workflow
// backends. Each activity performs one model request or one top-level tool call.
package durable

import (
	"context"
	"errors"

	h "github.com/natexcvi/go-llm"
)

// Input is the state of the agent and an optional decision for its first pending call.
type Input struct {
	Snapshot h.Snapshot `json:"snapshot"`
	Decision string     `json:"decision,omitempty"`
}
type StepResult struct {
	Snapshot h.Snapshot `json:"snapshot"`
	Approval *h.Call    `json:"approval,omitempty"`
	Paused   bool       `json:"paused,omitempty"`
}

func Advance[T any](ctx context.Context, a h.Agent[T], input Input, policy func(context.Context, h.Call) (h.Decision, error)) (StepResult, error) {
	s := input.Snapshot
	switch input.Decision {
	case "":
	case "approve", "deny":
		if !s.AwaitingApproval || len(s.Pending) == 0 {
			return StepResult{}, errors.New("unexpected approval decision")
		}
	default:
		return StepResult{}, errors.New("invalid approval decision")
	}
	options := h.Options{}
	var approval *h.Call
	if policy != nil || input.Decision != "" {
		first := true
		options.Authorize = func(ctx context.Context, c h.Call) (h.Decision, error) {
			top := first && len(s.Pending) > 0 && c == s.Pending[0]
			first = false
			d := h.Defer
			var err error
			if policy != nil {
				d, err = policy(ctx, c)
			}
			if err != nil {
				return d, err
			}
			if top && d == h.Defer {
				switch input.Decision {
				case "approve":
					return h.Approve, nil
				case "deny":
					return h.Deny, nil
				}
				approval = &c
			}
			// A denial from either the operator or the active policy takes precedence.
			if top && input.Decision == "deny" {
				return h.Deny, nil
			}
			return d, nil
		}
	}
	result, err := a.Advance(ctx, s, options)
	if errors.Is(err, h.ErrDeferred) {
		return StepResult{Snapshot: result.Snapshot, Approval: approval, Paused: true}, nil
	}
	return StepResult{Snapshot: result.Snapshot}, err
}
