package capabilities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	h "github.com/natexcvi/go-llm"
)

func InputGuardrail(check func(context.Context, string) error) h.Capability {
	return func(ctx context.Context, r *h.Run) error { return check(ctx, r.Prompt) }
}
func OutputGuardrail(check func(context.Context, string) error) h.Capability {
	return func(_ context.Context, r *h.Run) error {
		r.CheckOutput = append(r.CheckOutput, check)
		return nil
	}
}
func ToolGuardrail(check func(context.Context, h.Call) error) h.Capability {
	return wrap(func(next h.ToolHandler) h.ToolHandler {
		return func(ctx context.Context, c h.Call) (string, error) {
			if err := check(ctx, c); err != nil {
				return "", err
			}
			return next(ctx, c)
		}
	})
}
func wrap(w h.ToolMiddleware) h.Capability {
	return func(_ context.Context, r *h.Run) error {
		r.AroundTool = append(r.AroundTool, w)
		return nil
	}
}

// ToolBudgets returns a tool result when a per-run allowance has been exhausted.
func ToolBudgets(limits map[string]int, refuseRepeats ...string) h.Capability {
	limits = maps.Clone(limits)
	refuseRepeats = slices.Clone(refuseRepeats)
	return func(ctx context.Context, r *h.Run) error {
		counts := map[string]int{}
		seen := map[string]bool{}
		if err := h.BindState(r, "tool_budgets.counts", &counts); err != nil {
			return err
		}
		if err := h.BindState(r, "tool_budgets.seen", &seen); err != nil {
			return err
		}
		return wrap(func(next h.ToolHandler) h.ToolHandler {
			return func(ctx context.Context, c h.Call) (string, error) {
				if n, ok := limits[c.Name]; ok && counts[c.Name] >= n {
					return "Tool call budget exhausted for " + c.Name, nil
				}
				var key string
				if slices.Contains(refuseRepeats, c.Name) {
					key = c.Name + "\x00" + canonicalArgs(c.Arguments)
					if seen[key] {
						return "Identical tool call already attempted.", nil
					}
					seen[key] = true
				}
				counts[c.Name]++
				result, err := next(ctx, c)
				if errors.Is(err, h.ErrDeferred) {
					counts[c.Name]--
					if key != "" {
						delete(seen, key)
					}
				}
				return result, err
			}
		})(ctx, r)
	}
}
func canonicalArgs(s string) string {
	var v any
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	if !json.Valid([]byte(s)) || d.Decode(&v) != nil {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

var ErrLoop = errors.New("repeated tool call loop")

// DetectLoops stops a run on the nth consecutive identical call.
func DetectLoops(n int) h.Capability {
	return func(ctx context.Context, r *h.Run) error {
		if n < 2 {
			return errors.New("loop threshold must be at least two")
		}
		var last [2]string
		var count int
		if err := h.BindState(r, "detect_loops.last", &last); err != nil {
			return err
		}
		if err := h.BindState(r, "detect_loops.count", &count); err != nil {
			return err
		}
		return wrap(func(next h.ToolHandler) h.ToolHandler {
			return func(ctx context.Context, c h.Call) (string, error) {
				previous, lastCount := last, count
				key := [2]string{c.Name, canonicalArgs(c.Arguments)}
				if key != last {
					last = key
					count = 0
				}
				count++
				if count >= n {
					return "", fmt.Errorf("%w: %s", ErrLoop, c.Name)
				}
				result, err := next(ctx, c)
				if errors.Is(err, h.ErrDeferred) && last == key {
					last, count = previous, lastCount
				}
				return result, err
			}
		})(ctx, r)
	}
}
func ToolOutputLimits(maxChars int) h.Capability {
	return wrap(func(next h.ToolHandler) h.ToolHandler {
		return func(ctx context.Context, c h.Call) (string, error) {
			s, err := next(ctx, c)
			if err != nil {
				return "", err
			}
			return clamp(s, maxChars), nil
		}
	})
}
func clamp(s string, maxChars int) string {
	if maxChars <= 0 {
		return s
	}
	n := 0
	for i := range s {
		if n == maxChars {
			return s[:i] + "\n[truncated]"
		}
		n++
	}
	return s
}

type requestHook func(context.Context, *h.Request, h.Usage) error

func before(f requestHook) h.Capability {
	return func(_ context.Context, r *h.Run) error {
		r.BeforeRequest = append(r.BeforeRequest, f)
		return nil
	}
}
func CurrentTime(clock func() time.Time) h.Capability {
	if clock == nil {
		clock = time.Now
	}
	return SystemReminder(func(h.Usage) string { return "Current time: " + clock().Format(time.RFC3339) })
}
func SystemReminder(message func(h.Usage) string) h.Capability {
	return before(func(_ context.Context, q *h.Request, u h.Usage) error {
		if text := message(u); text != "" {
			q.Messages = append([]h.Message{{Role: h.System, Text: text}}, q.Messages...)
		}
		return nil
	})
}

// SlidingWindow keeps the latest user turns and their entire tool call exchanges.
func SlidingWindow(turns int) h.Capability {
	return before(func(_ context.Context, q *h.Request, _ h.Usage) error {
		if turns < 1 {
			return errors.New("window size must be positive")
		}
		count := 0
		for i := len(q.Messages) - 1; i >= 0; i-- {
			if q.Messages[i].Role != h.User {
				continue
			}
			count++
			if count == turns {
				var keep []h.Message
				for _, m := range q.Messages[:i] {
					if m.Role == h.System {
						keep = append(keep, m)
					}
				}
				q.Messages = append(keep, q.Messages[i:]...)
				break
			}
		}
		return nil
	})
}
func ClearToolResults(keep int) h.Capability {
	return before(func(_ context.Context, q *h.Request, _ h.Usage) error {
		count := 0
		for i := len(q.Messages) - 1; i >= 0; i-- {
			if q.Messages[i].Role == h.ToolResult {
				count++
				if count > keep {
					q.Messages[i].Text = "[earlier tool result cleared]"
				}
			}
		}
		return nil
	})
}
func ClampOversizedMessages(chars int) h.Capability {
	return before(func(_ context.Context, q *h.Request, _ h.Usage) error {
		for i := range q.Messages {
			q.Messages[i].Text = clamp(q.Messages[i].Text, chars)
		}
		return nil
	})
}
func RequireVerification(tool string) h.Capability {
	return func(ctx context.Context, r *h.Run) error {
		done := false
		if err := h.BindState(r, "verification."+tool, &done); err != nil {
			return err
		}
		r.CheckOutput = append(r.CheckOutput, func(context.Context, string) error {
			if !done {
				return h.Retry(fmt.Errorf("verify your answer with %s", tool))
			}
			return nil
		})
		return wrap(func(next h.ToolHandler) h.ToolHandler {
			return func(ctx context.Context, c h.Call) (string, error) {
				out, err := next(ctx, c)
				if err == nil && c.Name == tool {
					done = true
				}
				return out, err
			}
		})(ctx, r)
	}
}
func AskUser(ask func(context.Context, string) (string, error)) h.Capability {
	return h.Tools(h.NewTool("ask_user", "Ask the user a question and wait for their response.", func(ctx context.Context, in struct {
		Question string `json:"question"`
	}) (string, error) {
		return ask(ctx, in.Question)
	}))
}

// SubAgent delegates a task to a child agent with independent history and limits.
func SubAgent(name, description string, child h.Agent[string]) h.Capability {
	return func(ctx context.Context, r *h.Run) error {
		tool, err := DelegationTool(r, name, description, child)
		if err != nil {
			return err
		}
		return r.Register(tool)
	}
}

// DelegationTool binds a child agent to the current run. Capabilities can use
// the returned tool for programmatic calls through Run.InvokeTool.
func DelegationTool(r *h.Run, name, description string, child h.Agent[string]) (h.Tool, error) {
	invoke, err := bindDelegate(r, name, Delegate{Agent: child}, false, false)
	if err != nil {
		return h.Tool{}, err
	}
	var taskID string
	tool := h.NewTool(name, description, func(ctx context.Context, in struct {
		Task string `json:"task"`
	}) (string, error) {
		return invoke(ctx, taskID, in.Task, nil)
	})
	execute := tool.Execute
	tool.Execute = func(ctx context.Context, c h.Call) (string, error) {
		taskID = c.ID
		return execute(ctx, c)
	}
	return tool, nil
}
