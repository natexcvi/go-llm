// Package capabilities provides composable run policies and tools.
package capabilities

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	h "github.com/natexcvi/go-llm"
)

// ContextBudget uses a provider usage anchor when history contains one. For a
// cold history it estimates text, tool schemas and request instructions. Images
// use a fixed estimate until the first provider usage report.
type ContextBudget struct {
	Window    int
	CountText func(string) int
}

type ContextUsage struct {
	UsedTokens, WindowTokens int
	Fraction                 float64
	WindowKnown              bool
}

func (b ContextBudget) count(s string) int {
	if s == "" {
		return 0
	}
	if b.CountText != nil {
		return max(0, b.CountText(s))
	}
	var quarters int
	for _, c := range s {
		switch {
		case c > 0xffff:
			quarters += 12
		case c >= 128:
			quarters += 4
		case strings.ContainsRune("{}[],:\"", c):
			quarters += 5
		default:
			quarters++
		}
	}
	return (quarters + 3) / 4
}

func (b ContextBudget) message(m h.Message) int {
	n := 4 + b.count(m.Text) + b.count(m.CallID) + 1024*len(m.Images)
	if m.Native != nil {
		n += max(m.Native.Tokens, b.count(m.Native.Content))
	}
	for _, c := range m.Calls {
		n += 4 + b.count(c.Name) + b.count(c.ID) + b.count(c.Arguments)
	}
	return n
}

func (b ContextBudget) Measure(q h.Request, instructions string) ContextUsage {
	n := b.count(instructions) + b.count(string(q.OutputSchema))
	for _, t := range q.Tools {
		n += 4 + b.count(t.Name) + b.count(t.Description) + b.count(string(t.Schema))
	}
	for _, m := range q.Messages {
		if m.ContextTokens > 0 {
			n = m.ContextTokens
		} else {
			n += b.message(m)
		}
	}
	window := b.Window
	if window <= 0 {
		window = 200000
	}
	return ContextUsage{UsedTokens: n, WindowTokens: window, Fraction: float64(n) / float64(window), WindowKnown: b.Window > 0}
}

// CompactionStrategy returns a replacement history, or nil when there is
// nothing to compact. Treat input as read-only. Focus is set by compact_context.
type CompactionStrategy func(ctx context.Context, history []h.Message, focus string) ([]h.Message, error)

type CompactionPolicy struct {
	Budget ContextBudget
	// Set either a token target or a fraction of the context window.
	// MaxMessages is an optional additional trigger and stop target.
	MaxMessages    int
	TargetTokens   int
	TargetFraction float64
	Strategies     []CompactionStrategy
	// OnRequest offers compact_context. A manual-only policy needs no target.
	OnRequest bool
}

func (p CompactionPolicy) target() (int, error) {
	if p.MaxMessages < 0 || p.Budget.Window < 0 || p.TargetTokens < 0 || math.IsNaN(p.TargetFraction) || p.TargetFraction < 0 || p.TargetFraction > 1 || (p.TargetTokens > 0 && p.TargetFraction > 0) {
		return 0, errors.New("invalid compaction budget")
	}
	if len(p.Strategies) == 0 || slices.ContainsFunc(p.Strategies, func(s CompactionStrategy) bool { return s == nil }) {
		return 0, errors.New("compaction requires strategies")
	}
	if p.TargetFraction > 0 {
		window := p.Budget.Measure(h.Request{}, "").WindowTokens
		return max(1, int(p.TargetFraction*float64(window))), nil
	}
	if p.TargetTokens == 0 && p.MaxMessages == 0 && !p.OnRequest {
		return 0, errors.New("supply a compaction target or enable OnRequest")
	}
	return p.TargetTokens, nil
}

// CompactNow runs the policy outside an agent. It always tries the first tier,
// even below target. A manual-only policy stops after the first effective tier.
// A nil result means the history is already as small as the strategies allow.
func (p CompactionPolicy) CompactNow(ctx context.Context, q h.Request, instructions, focus string) ([]h.Message, error) {
	target, err := p.target()
	if err != nil {
		return nil, err
	}
	return p.compact(ctx, q, instructions, focus, target, true)
}

func (p CompactionPolicy) compact(ctx context.Context, q h.Request, instructions, focus string, target int, force bool) ([]h.Message, error) {
	used := p.Budget.Measure(q, instructions).UsedTokens
	var result []h.Message
	for _, strategy := range p.Strategies {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !force && (target == 0 || used <= target) && (p.MaxMessages == 0 || len(q.Messages) <= p.MaxMessages) {
			break
		}
		updated, err := strategy(ctx, q.Messages, focus)
		if err != nil {
			return nil, err
		}
		if updated == nil {
			continue
		}
		// A summary larger than its source spends context without reclaiming it.
		delta := 0
		for _, m := range q.Messages {
			delta += p.Budget.message(m)
		}
		for _, m := range updated {
			delta -= p.Budget.message(m)
		}
		if delta < 0 || (delta == 0 && len(updated) >= len(q.Messages)) {
			continue
		}
		updated = slices.Clone(updated)
		for i := range updated {
			updated[i].ContextTokens = 0
		}
		q.Messages = updated
		used = max(p.Budget.Measure(q, instructions).UsedTokens, used-delta)
		if len(updated) > 0 {
			updated[len(updated)-1].ContextTokens = used
		}
		result = updated
		force = false
	}
	return result, nil
}

func (p CompactionPolicy) Capability() h.Capability {
	p.Strategies = slices.Clone(p.Strategies)
	return func(_ context.Context, r *h.Run) error {
		target, err := p.target()
		if err != nil {
			return err
		}
		var requested *string
		if p.OnRequest {
			if err := h.BindState(r, "compact_context.focus", &requested); err != nil {
				return err
			}
			tool := h.NewTool("compact_context", "Compact earlier context before the next request. Optionally specify what to preserve.", func(_ context.Context, args struct {
				Focus string `json:"focus,omitempty"`
			}) (string, error) {
				requested = &args.Focus
				return "Compaction requested.", nil
			})
			if err := r.Register(tool); err != nil {
				return err
			}
		}
		r.RewriteHistory = append(r.RewriteHistory, func(ctx context.Context, q h.Request, _ h.Usage) ([]h.Message, error) {
			focus := ""
			force := requested != nil
			if force {
				focus = *requested
			}
			updated, err := p.compact(ctx, q, strings.Join(r.Instructions, "\n\n"), focus, target, force)
			if err == nil {
				requested = nil
			}
			return updated, err
		})
		return nil
	}
}

// ReportContextUsage observes the model-facing request after preceding hooks.
func ReportContextUsage(b ContextBudget, observe func(context.Context, ContextUsage) error) h.Capability {
	return before(func(ctx context.Context, q *h.Request, _ h.Usage) error {
		if observe == nil {
			return errors.New("context usage observer required")
		}
		return observe(ctx, b.Measure(*q, ""))
	})
}

// WarnNearLimits adds an ephemeral warning at either fraction of the window.
func WarnNearLimits(b ContextBudget, urgent, critical float64) h.Capability {
	return before(func(_ context.Context, q *h.Request, _ h.Usage) error {
		if !(urgent > 0 && urgent < critical && critical <= 1) {
			return errors.New("invalid context warning thresholds")
		}
		u := b.Measure(*q, "")
		severity := "URGENT"
		if u.Fraction < urgent {
			return nil
		}
		if u.Fraction >= critical {
			severity = "CRITICAL"
		}
		q.Messages = append(q.Messages, h.Message{Role: h.User, Text: fmt.Sprintf("%s: estimated context usage %.0f%%. Preserve essential findings and wrap up.", severity, 100*u.Fraction)})
		return nil
	})
}

const clearedResult = "[earlier tool result cleared]"

func protectedCall(name string) bool {
	return name == "load_capability" || name == "search_tools" || name == "tool_search" || name == "compact_context" || name == "prune_tool_result"
}

// resultCalls matches IDs within their own assistant response. Providers may
// reuse an ID in a later round.
func resultCalls(history []h.Message) map[int]h.Call {
	out := map[int]h.Call{}
	var calls map[string]h.Call
	for i, m := range history {
		switch m.Role {
		case h.Assistant:
			calls = map[string]h.Call{}
			if !m.Pinned {
				for _, c := range m.Calls {
					calls[c.ID] = c
				}
			}
		case h.ToolResult:
			if c, ok := calls[m.CallID]; ok && !m.Pinned && !protectedCall(c.Name) {
				out[i] = c
			}
		default:
			calls = nil
		}
	}
	return out
}

// ClearOldResults preserves the newest keep results and rewrites older results
// in batches to reduce the frequency of provider prefix-cache invalidation.
func ClearOldResults(keep, batch int) CompactionStrategy {
	return func(_ context.Context, history []h.Message, _ string) ([]h.Message, error) {
		if keep < 0 || batch < 1 {
			return nil, errors.New("invalid result retention")
		}
		calls := resultCalls(history)
		var candidates []int
		for i := len(history) - 1; i >= 0; i-- {
			if _, ok := calls[i]; ok {
				candidates = append(candidates, i)
			}
		}
		var edits []int
		for _, i := range candidates[min(keep, len(candidates)):] {
			if len(history[i].Text) > len(clearedResult) && history[i].Text != clearedResult {
				edits = append(edits, i)
			}
		}
		if len(edits) < batch {
			return nil, nil
		}
		out := slices.Clone(history)
		for _, i := range edits {
			out[i].Text = clearedResult
		}
		return out, nil
	}
}

// DeduplicateFileReads uses the caller's key to identify interchangeable reads.
// Include offsets and ranges in the key when the read operation is partial.
func DeduplicateFileReads(key func(h.Call) string) CompactionStrategy {
	return func(_ context.Context, history []h.Message, _ string) ([]h.Message, error) {
		if key == nil {
			return nil, errors.New("file key required")
		}
		calls := resultCalls(history)
		seen := map[string]bool{}
		var out []h.Message
		for i := len(history) - 1; i >= 0; i-- {
			c, ok := calls[i]
			if !ok {
				continue
			}
			k := key(c)
			if k == "" {
				continue
			}
			if seen[k] && history[i].Text != clearedResult && len(history[i].Text) > len(clearedResult) {
				if out == nil {
					out = slices.Clone(history)
				}
				out[i].Text = clearedResult
			}
			seen[k] = true
		}
		return out, nil
	}
}

// RetainRecent keeps the latest user request and the last keep message groups.
// An assistant call and all its returns form one group. Pinned groups, system
// instructions and tool discovery survive. An optional summarizer receives
// only the removed groups, in order.
func RetainRecent(keep int, summarize func(context.Context, []h.Message, string) (string, error)) CompactionStrategy {
	return func(ctx context.Context, history []h.Message, focus string) ([]h.Message, error) {
		if keep < 1 {
			return nil, errors.New("retain at least one group")
		}
		var starts []int
		latestUser := -1
		for i, m := range history {
			if m.Role != h.ToolResult || i == 0 {
				starts = append(starts, i)
			}
			if m.Role == h.User && m.ContextKind == "" {
				latestUser = i
			}
		}
		memory := latestMemory(history)
		var out, removed []h.Message
		insert := -1
		for j, start := range starts {
			end := len(history)
			if j+1 < len(starts) {
				end = starts[j+1]
			}
			group := history[start:end]
			keepGroup := j >= len(starts)-keep || start == latestUser || memory[start]
			for _, m := range group {
				if m.Pinned || m.Role == h.System || slices.ContainsFunc(m.Calls, func(c h.Call) bool {
					return protectedCall(c.Name) && c.Name != "compact_context" && c.Name != "prune_tool_result"
				}) {
					keepGroup = true
				}
			}
			if keepGroup {
				out = append(out, group...)
			} else {
				if insert < 0 {
					insert = len(out)
				}
				removed = append(removed, group...)
			}
		}
		if len(removed) == 0 {
			return nil, nil
		}
		if summarize != nil {
			s, err := summarize(ctx, removed, focus)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(s) == "" {
				return nil, errors.New("empty conversation summary")
			}
			out = slices.Insert(out, insert, h.Message{Role: h.User, ContextKind: "summary", Text: "Summary of earlier conversation:\n" + s})
		}
		return out, nil
	}
}

// FallbackCompaction tries the next strategy only for errors selected by retry.
// Context cancellation always stops the chain.
func FallbackCompaction(retry func(error) bool, strategies ...CompactionStrategy) CompactionStrategy {
	chain := slices.Clone(strategies)
	return func(ctx context.Context, history []h.Message, focus string) ([]h.Message, error) {
		var failures []error
		if len(chain) == 0 {
			return nil, errors.New("empty fallback chain")
		}
		for _, s := range chain {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if s == nil {
				return nil, errors.New("nil fallback strategy")
			}
			out, err := s(ctx, history, focus)
			if err == nil {
				return out, nil
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if retry == nil || !retry(err) {
				return nil, err
			}
			failures = append(failures, err)
		}
		return nil, errors.Join(failures...)
	}
}

func pruneResultTool() h.Capability {
	return func(_ context.Context, r *h.Run) error {
		return r.Register(h.NewTool("prune_tool_result", "Replace a result seen on the last request with a shorter summary.", func(ctx context.Context, args struct {
			ID      string `json:"tool_call_id"`
			Summary string `json:"summary"`
		}) (string, error) {
			err := r.EditAnsweredToolResult(ctx, args.ID, func(call h.Call, old string) (string, error) {
				replacement := "[tool result pruned] " + strings.TrimSpace(args.Summary)
				if protectedCall(call.Name) || len(replacement) >= len(old) {
					return "", errors.New("result is protected or summary is not shorter")
				}
				return replacement, nil
			})
			if err != nil {
				return "Pruning refused: " + err.Error(), nil
			}
			return "Result pruned.", nil
		}))
	}
}

// NativeCompactor issues a provider-specific summary and returns the opaque
// assistant message required for continuation on that transport.
type NativeCompactor interface {
	Compact(context.Context, h.Request, string) (h.Message, error)
}

// NativeSummary compacts a contiguous prefix on the provider and preserves
// the current user request, pinned exchanges, instruction and discovery data.
func NativeSummary(model NativeCompactor, keep int) CompactionStrategy {
	return func(ctx context.Context, history []h.Message, focus string) ([]h.Message, error) {
		if model == nil || keep < 1 {
			return nil, errors.New("native compaction needs a model and a positive tail size")
		}
		var starts []int
		lastUser := len(history)
		for i, m := range history {
			if m.Role != h.ToolResult {
				starts = append(starts, i)
			}
			if m.Role == h.User && m.ContextKind == "" {
				lastUser = i
			}
		}
		if len(starts) <= keep {
			return nil, nil
		}
		end := starts[len(starts)-keep]
		if end < 2 {
			return nil, nil
		}
		// Anthropic ignores material before its compaction block. Replay pinned
		// groups after the block, so they still reach the continuing model.
		memory := latestMemory(history)
		var instructions, retained []h.Message
		reducible := false
		for j, start := range starts[:len(starts)-keep] {
			group := history[start:starts[j+1]]
			if group[0].Role == h.System {
				instructions = append(instructions, group...)
				continue
			}
			protected := start == lastUser || memory[start] || slices.ContainsFunc(group, func(m h.Message) bool {
				return m.Pinned || slices.ContainsFunc(m.Calls, func(c h.Call) bool { return protectedCall(c.Name) })
			})
			if protected {
				retained = append(retained, group...)
			} else if group[0].Native == nil {
				reducible = true
			}
		}
		if !reducible {
			return nil, nil
		}
		summary, err := model.Compact(ctx, h.Request{Messages: history[:end]}, focus)
		if err != nil {
			return nil, err
		}
		if summary.Native == nil || summary.Role != h.Assistant {
			return nil, errors.New("missing native compaction item")
		}
		out := append(instructions, summary)
		out = append(out, retained...)
		return append(out, history[end:]...), nil
	}
}

func latestMemory(history []h.Message) map[int]bool {
	keep := map[int]bool{}
	seen := map[string]bool{}
	for i := len(history) - 1; i >= 0; i-- {
		kind := history[i].ContextKind
		if strings.HasPrefix(kind, "memory:") && !seen[kind] {
			keep[i] = true
			seen[kind] = true
		}
	}
	return keep
}
