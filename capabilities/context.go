package capabilities

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/storage"
)

// StreamGuardrail checks the text generated in the current response before a
// delta reaches the observer. It also checks complete non-streaming outputs.
func StreamGuardrail(check func(context.Context, string) error) h.Capability {
	return func(ctx context.Context, r *h.Run) error {
		var text strings.Builder
		r.BeforeRequest = append(r.BeforeRequest, func(context.Context, *h.Request, h.Usage) error {
			text.Reset()
			return nil
		})
		r.CheckDelta = append(r.CheckDelta, func(ctx context.Context, delta string) error {
			text.WriteString(delta)
			return check(ctx, text.String())
		})
		r.CheckOutput = append(r.CheckOutput, check)
		return nil
	}
}

// Goal asks the model to correct its output until verify accepts it or the
// run's correction budget runs out.
func Goal(description string, verify func(context.Context, string) (string, error)) h.Capability {
	return func(ctx context.Context, r *h.Run) error {
		r.Instructions = append(r.Instructions, description)
		r.CheckOutput = append(r.CheckOutput, func(ctx context.Context, text string) error {
			feedback, err := verify(ctx, text)
			if err != nil {
				return err
			}
			if feedback != "" {
				return h.Retry(errors.New(feedback))
			}
			return nil
		})
		return nil
	}
}

// ModelRouter selects the model for each request using the prepared conversation.
func ModelRouter(selectModel func(context.Context, h.Request, h.Usage) (h.Model, error)) h.Capability {
	return func(ctx context.Context, r *h.Run) error {
		r.BeforeRequest = append(r.BeforeRequest, func(ctx context.Context, q *h.Request, u h.Usage) error {
			model, err := selectModel(ctx, *q, u)
			if err != nil {
				return err
			}
			if model == nil {
				return errors.New("router selected nil model")
			}
			r.Model = model
			return nil
		})
		return nil
	}
}
func ReportRemainingBudget() h.Capability {
	return func(ctx context.Context, r *h.Run) error {
		return SystemReminder(func(u h.Usage) string {
			items := []string{fmt.Sprintf("%d requests", r.Limits.Requests-u.Requests)}
			if n := r.Limits.ToolCalls; n > 0 {
				items = append(items, fmt.Sprintf("%d tool calls", max(0, n-u.ToolCalls)))
			}
			if n := r.Limits.TotalTokens; n > 0 {
				items = append(items, fmt.Sprintf("%d tokens", max(0, n-u.InputTokens-u.OutputTokens)))
			}
			return "Remaining run budget: " + strings.Join(items, ", ") + "."
		})(ctx, r)
	}
}

// WrapUpAtLimits withdraws tools on the last allowed request so the model
// has a chance to return a final answer.
func WrapUpAtLimits() h.Capability {
	return func(ctx context.Context, r *h.Run) error {
		return before(func(_ context.Context, q *h.Request, u h.Usage) error {
			if u.Requests+1 >= r.Limits.Requests || (r.Limits.ToolCalls > 0 && u.ToolCalls >= r.Limits.ToolCalls) {
				q.Tools = nil
				q.Messages = append(q.Messages, h.Message{Role: h.User, Text: "Return your final answer using the evidence gathered so far."})
			}
			return nil
		})(ctx, r)
	}
}

// PruneToolResults offers targeted, persistent pruning and suppresses repeated
// identical results in the outgoing request.
func PruneToolResults() h.Capability {
	return h.Combine(pruneResultTool(), before(func(_ context.Context, q *h.Request, _ h.Usage) error {
		seen := map[string]bool{}
		eligible := resultCalls(q.Messages)
		for i := len(q.Messages) - 1; i >= 0; i-- {
			m := &q.Messages[i]
			if _, ok := eligible[i]; !ok {
				continue
			}
			if seen[m.Text] {
				m.Text = "[duplicate of a later tool result]"
			} else {
				seen[m.Text] = true
			}
		}
		return nil
	}))
}

// SummarizingCompaction condenses earlier turns using an application-provided
// summarizer. It retains complete user turns and tool exchanges.
func SummarizingCompaction(keepTurns int, summarize func(context.Context, []h.Message) (string, error)) h.Capability {
	return before(func(ctx context.Context, q *h.Request, _ h.Usage) error {
		if keepTurns < 1 {
			return errors.New("keepTurns must be positive")
		}
		cut := 0
		turns := 0
		for i := len(q.Messages) - 1; i >= 0; i-- {
			if q.Messages[i].Role == h.User {
				turns++
				if turns == keepTurns {
					cut = i
					break
				}
			}
		}
		var earlier, sys []h.Message
		for _, m := range q.Messages[:cut] {
			if m.Role == h.System {
				sys = append(sys, m)
			} else {
				earlier = append(earlier, m)
			}
		}
		if len(earlier) == 0 {
			return nil
		}
		s, err := summarize(ctx, earlier)
		if err != nil {
			return err
		}
		sys = append(sys, h.Message{Role: h.User, Text: "Summary of earlier conversation:\n" + s})
		q.Messages = append(sys, q.Messages[cut:]...)
		return nil
	})
}

type overflowArgs struct {
	Key    string `json:"key"`
	Offset int    `json:"offset,omitempty"`
}

// SpillToolOutput retains long results in a dedicated store and gives the
// model a bounded reader. SHA-256 keys make repeated writes idempotent.
func SpillToolOutput(store storage.Store, chars int) h.Capability {
	return func(ctx context.Context, r *h.Run) error {
		if chars < 1 || store == nil {
			return errors.New("spill requires a store and a positive character limit")
		}
		read := h.NewTool("read_tool_output", "Read the next section of a stored tool result by key and character offset.", func(ctx context.Context, a overflowArgs) (string, error) {
			if len(a.Key) != 64 {
				return "", h.Retry(errors.New("invalid tool output key"))
			}
			if _, err := hex.DecodeString(a.Key); err != nil {
				return "", h.Retry(err)
			}
			b, err := store.Get(ctx, a.Key)
			if err != nil {
				return "", err
			}
			runes := []rune(string(b))
			if a.Offset < 0 || a.Offset > len(runes) {
				return "", h.Retry(errors.New("invalid offset"))
			}
			return string(runes[a.Offset : a.Offset+min(len(runes)-a.Offset, chars)]), nil
		})
		if err := r.Register(read); err != nil {
			return err
		}
		return wrap(func(next h.ToolHandler) h.ToolHandler {
			return func(ctx context.Context, c h.Call) (string, error) {
				out, err := next(ctx, c)
				if err != nil || c.Name == read.Name || utf8.RuneCountInString(out) <= chars {
					return out, err
				}
				digest := sha256.Sum256([]byte(out))
				key := hex.EncodeToString(digest[:])
				if err = store.Put(ctx, key, []byte(out)); err != nil {
					return "", err
				}
				return fmt.Sprintf("%s\nFull result: read_tool_output key %s, offset %d.", clamp(out, chars), key, chars), nil
			}
		})(ctx, r)
	}
}

// Permissions applies the first matching rule. The returned callback is used
// as Options.Authorize and is reapplied when a deferred run resumes.
type PermissionRule struct {
	Tools    []string
	Match    func(h.Call) bool
	Decision h.Decision
}

func Permissions(rules []PermissionRule, fallback h.Decision) func(context.Context, h.Call) (h.Decision, error) {
	rules = slices.Clone(rules)
	return func(_ context.Context, c h.Call) (h.Decision, error) {
		for _, rule := range rules {
			if len(rule.Tools) > 0 && !slices.Contains(rule.Tools, c.Name) {
				continue
			}
			if rule.Match == nil || rule.Match(c) {
				return rule.Decision, nil
			}
		}
		return fallback, nil
	}
}
