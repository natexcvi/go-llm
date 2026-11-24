package harness

import (
	"context"
	"fmt"
	"slices"
)

// ChildOptions inherits authorization and optionally forwards events and usage.
// Use synchronously inside a delegated tool. A shared account charges each
// operation as it occurs, including work done before a child suspends.
func (r *Run) ChildOptions(ctx context.Context, name string, shareUsage, forwardEvents bool) (Options, error) {
	if ctx.Value(activeRunKey{}) != r || r.childOptions == nil {
		return Options{}, fmt.Errorf("child options require an active tool call")
	}
	return r.childOptions(name, shareUsage, forwardEvents), nil
}

type usageAccount struct {
	usage    *Usage
	limits   Limits
	reserved func() int
}

func (x *execution[T]) childOptions(name string, shared, events bool) Options {
	opts := Options{Authorize: x.run.Authorize}
	if shared {
		opts.ancestors = append(slices.Clone(x.opts.ancestors), usageAccount{&x.result.Usage, x.agent.Limits, func() int { return len(x.result.Pending) + x.nestedDepth }})
	}
	if events && x.opts.Observe != nil {
		opts.Observe = func(ctx context.Context, e Event) error {
			e.Delegates = append([]string{name}, e.Delegates...)
			return x.opts.Observe(ctx, e)
		}
	}
	return opts
}
func (x *execution[T]) charge(delta Usage) {
	add := func(u *Usage) {
		u.Requests += delta.Requests
		u.InputTokens += delta.InputTokens
		u.OutputTokens += delta.OutputTokens
		u.ToolCalls += delta.ToolCalls
	}
	add(&x.result.Usage)
	for _, a := range x.opts.ancestors {
		add(a.usage)
	}
}
func (x *execution[T]) checkAncestors(requests, tools int) error {
	// Reserve each enclosing unfinished tool so a child cannot spend the
	// allowance needed to commit its own delegation.
	for i := len(x.opts.ancestors) - 1; i >= 0; i-- {
		a := x.opts.ancestors[i]
		tools += a.reserved()
		u := a.usage
		l := a.limits
		if exceeds(u.Requests+requests, l.Requests) || exceeds(u.ToolCalls+tools, l.ToolCalls) || exceeds(u.InputTokens, l.InputTokens) || exceeds(u.OutputTokens, l.OutputTokens) || exceeds(u.InputTokens+u.OutputTokens, l.TotalTokens) {
			return fmt.Errorf("%w: shared delegation budget", ErrLimit)
		}
	}
	return nil
}
