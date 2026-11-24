package harness

import "context"

// AccountModelRequest records a helper model request made by a history
// processor. The operation returns the provider's input and output usage.
// Outside a run it executes directly. Within a run, helper requests share the
// normal request and token limits, including the enclosing delegation budget.
func AccountModelRequest(ctx context.Context, op func(context.Context) (Usage, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if account, ok := ctx.Value(auxiliaryKey{}).(auxiliaryAccount); ok {
		return account(ctx, op)
	}
	_, err := op(ctx)
	return err
}

type auxiliaryKey struct{}
type auxiliaryAccount func(context.Context, func(context.Context) (Usage, error)) error

func (x *execution[T]) auxiliaryRequest(ctx context.Context, op func(context.Context) (Usage, error)) error {
	if err := x.requestAllowed(); err != nil {
		return err
	}
	x.charge(Usage{Requests: 1})
	u, err := op(ctx)
	x.charge(Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens})
	if err != nil {
		return err
	}
	return x.tokensAllowed()
}
