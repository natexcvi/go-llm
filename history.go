package harness

import (
	"context"
	"errors"
	"fmt"
)

// EditAnsweredToolResult replaces one result from the tool batch seen by the
// latest model response. It is available during tool execution. The change
// is persisted with the executing tool's checkpoint.
func (r *Run) EditAnsweredToolResult(ctx context.Context, id string, edit func(Call, string) (string, error)) error {
	if ctx.Value(activeRunKey{}) != r || r.editAnsweredResult == nil || edit == nil {
		return errors.New("result editing requires an active tool call")
	}
	return r.editAnsweredResult(id, edit)
}

func (x *execution[T]) editAnsweredResult(id string, edit func(Call, string) (string, error)) error {
	history := x.result.History
	i := len(history) - 1
	for i >= 0 && history[i].Role == ToolResult {
		i--
	} // results of the current batch
	if len(x.result.Pending) == 0 || i < 0 || history[i].Role != Assistant {
		return errors.New("no active tool batch")
	}
	end := i
	for i > 0 && history[i-1].Role == ToolResult {
		i--
	}
	if i > 0 && history[i-1].Role == Assistant && !history[i-1].Pinned {
		for _, call := range history[i-1].Calls {
			if call.ID != id {
				continue
			}
			for j := i; j < end; j++ {
				if history[j].CallID != id || history[j].Pinned {
					continue
				}
				text, err := edit(call, history[j].Text)
				if err != nil {
					return err
				}
				history[j].Text = text
				for k := j; k < len(history); k++ {
					history[k].ContextTokens = 0
				}
				return nil
			}
		}
	}
	return fmt.Errorf("result %q is unavailable for editing in this batch", id)
}
