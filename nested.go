package harness

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

type activeRunKey struct{}

// InvokeTool calls an explicitly supplied nested tool from inside a tool
// handler. Invoke synchronously in the handler's goroutine. The application
// authorizes the nested call, and run middleware, limits and events see it.
func (r *Run) InvokeTool(ctx context.Context, t Tool, c Call) (string, error) {
	if r.invoke == nil || ctx.Value(activeRunKey{}) != r {
		return "", errors.New("nested tool call outside active tool handler")
	}
	if t.Name != c.Name || t.Execute == nil || c.ID == "" {
		return "", errors.New("invalid nested tool call")
	}
	return r.invoke(ctx, t, c)
}
func (x *execution[T]) executeTool(ctx context.Context, c Call) (string, error) {
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	return x.handler(context.WithValue(callCtx, activeRunKey{}, x.run), c)
}

func (x *execution[T]) invokeNested(ctx context.Context, t Tool, c Call) (string, error) {
	if err := x.checkAncestors(0, len(x.result.Pending)+x.nestedDepth+1); err != nil {
		return "", err
	}
	if exceeds(x.result.Usage.ToolCalls+len(x.result.Pending)+x.nestedDepth+1, x.agent.Limits.ToolCalls) {
		return "", fmt.Errorf("%w: nested tool calls", ErrLimit)
	}
	if err := x.emit(ctx, Event{Kind: "tool_call", Call: c}); err != nil {
		return "", err
	}
	d := Approve
	if x.opts.Authorize != nil {
		var err error
		d, err = x.opts.Authorize(ctx, c)
		if err != nil {
			return "", err
		}
	}
	var text string
	var err error
	switch d {
	case Approve:
		x.nestedDepth++
		defer func() { x.nestedDepth-- }()
		handler := ToolHandler(t.Execute)
		for _, wrap := range slices.Backward(x.run.AroundTool) {
			handler = wrap(handler)
		}
		text, err = handler(ctx, c)
		if errors.Is(err, ErrDeferred) {
			return "", err
		}
	case Deny:
		text = "Tool call denied by application policy."
	case Defer:
		return "", fmt.Errorf("%w: %s", ErrDeferred, c.ID)
	default:
		return "", errors.New("invalid nested tool authorization")
	}
	x.charge(Usage{ToolCalls: 1})
	if err != nil {
		return "", err
	}
	if err := x.emit(ctx, Event{Kind: "tool_result", Call: c, Text: text}); err != nil {
		return "", err
	}
	return text, nil
}
