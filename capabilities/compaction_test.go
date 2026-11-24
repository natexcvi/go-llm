package capabilities_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	h "github.com/natexcvi/go-llm"
	c "github.com/natexcvi/go-llm/capabilities"
)

func TestCompactionUsesProviderAnchorAndStopsAfterCheapTier(t *testing.T) {
	history := []h.Message{
		{Role: h.User, Text: "find it"},
		{Role: h.Assistant, Calls: []h.Call{{ID: "x", Name: "lookup", Arguments: "{}"}}},
		{Role: h.ToolResult, CallID: "x", Text: strings.Repeat("a", 400)},
		{Role: h.Assistant, Text: "found it", ContextTokens: 300},
	}
	fallback := func(context.Context, []h.Message, string) ([]h.Message, error) {
		t.Fatal("unnecessary escalation")
		return nil, nil
	}
	p := c.CompactionPolicy{Budget: c.ContextBudget{Window: 400}, TargetFraction: .6, Strategies: []c.CompactionStrategy{c.ClearOldResults(0, 1), fallback}}
	var measured c.ContextUsage
	a := h.Agent[string]{Instructions: "fixed", Output: h.Text(), Capabilities: []h.Capability{p.Capability(), c.ReportContextUsage(p.Budget, func(_ context.Context, u c.ContextUsage) error { measured = u; return nil })}, Model: h.ModelFunc(func(ctx context.Context, q h.Request) (h.Response, error) {
		if q.Messages[3].Text != "[earlier tool result cleared]" || q.Messages[3].CallID != "x" {
			t.Fatal(q.Messages)
		}
		return h.Response{Text: "ok"}, nil
	})}
	res, err := a.Run(t.Context(), "continue", h.Options{History: history})
	if err != nil || res.History[2].Text != "[earlier tool result cleared]" || measured.UsedTokens > 240 || measured.UsedTokens < 160 || measured.WindowTokens != 400 || history[2].Text != strings.Repeat("a", 400) {
		t.Fatal(res, measured, err)
	}
}

func TestManualCompactionSurvivesDurableSteps(t *testing.T) {
	summaries := 0
	p := c.CompactionPolicy{OnRequest: true, Strategies: []c.CompactionStrategy{c.RetainRecent(1, func(_ context.Context, old []h.Message, focus string) (string, error) {
		if focus != "invoice" || old[0].Text != strings.Repeat("old ", 100) {
			t.Fatal(old, focus)
		}
		summaries++
		return "saved invoice number 34", nil
	})}}
	modelCalls := 0
	a := h.Agent[string]{Output: h.Text(), Capabilities: []h.Capability{p.Capability()}, Model: h.ModelFunc(func(_ context.Context, q h.Request) (h.Response, error) {
		modelCalls++
		if modelCalls == 1 {
			return h.Response{Calls: []h.Call{{ID: "compact", Name: "compact_context", Arguments: `{"focus":"invoice"}`}}}, nil
		}
		if !slices.ContainsFunc(q.Messages, func(m h.Message) bool { return m.ContextKind == "summary" && strings.Contains(m.Text, "34") }) || !slices.ContainsFunc(q.Messages, func(m h.Message) bool { return m.Text == "continue" }) {
			t.Fatal(q.Messages)
		}
		return h.Response{Text: "complete"}, nil
	})}
	s := h.Start("continue", []h.Message{{Role: h.User, Text: strings.Repeat("old ", 100)}, {Role: h.Assistant, Text: "34"}})
	for i := 0; i < 4 && !s.Complete; i++ {
		r, err := a.Advance(t.Context(), s, h.Options{})
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(r.Snapshot)
		if err != nil {
			t.Fatal(err)
		}
		s = h.Snapshot{}
		if err := json.Unmarshal(b, &s); err != nil {
			t.Fatal(err)
		}
	}
	if !s.Complete || summaries != 1 || modelCalls != 2 {
		t.Fatal(s, summaries, modelCalls)
	}
}

func TestRetentionAndFileDedup(t *testing.T) {
	ctx := t.Context()
	history := []h.Message{{Role: h.User, Text: "goal"}}
	for _, call := range []h.Call{{Name: "read", ID: "x"}, {Name: "read", ID: "x"}, {Name: "load_capability", ID: "skill"}, {Name: "read", ID: "pinned"}, {Name: "last", ID: "z"}} {
		history = append(history, h.Message{Role: h.Assistant, Calls: []h.Call{call}}, h.Message{Role: h.ToolResult, CallID: call.ID, Pinned: call.ID == "pinned", Text: strings.Repeat(call.Name, 50)})
	}
	d, err := c.DeduplicateFileReads(func(call h.Call) string {
		if call.Name == "read" {
			return "file"
		}
		return ""
	})(ctx, history, "")
	if err != nil || !strings.Contains(d[2].Text, "cleared") || d[4].Text != history[4].Text || d[8].Text != history[8].Text {
		t.Fatal(d, err)
	}
	keep, err := c.RetainRecent(1, nil)(ctx, history, "")
	if err != nil || len(keep) != 7 || keep[0].Text != "goal" || keep[2].CallID != "skill" || keep[4].CallID != "pinned" || keep[6].CallID != "z" {
		t.Fatal(keep, err)
	}
	batch, err := c.ClearOldResults(1, 3)(ctx, history, "")
	if err != nil || batch != nil {
		t.Fatal(batch, err)
	}
	oops := errors.New("summary service unavailable")
	fail := func(context.Context, []h.Message, string) ([]h.Message, error) { return nil, oops }
	f := c.FallbackCompaction(func(e error) bool { return errors.Is(e, oops) }, fail, c.RetainRecent(1, nil))
	if got, err := f(ctx, history, ""); err != nil || len(got) != len(keep) {
		t.Fatal(got, err)
	}
}

func TestPruneLandsBeforeDeferredSibling(t *testing.T) {
	long := strings.Repeat("haystack ", 80)
	lookup := h.NewTool("lookup", "", func(context.Context, struct{}) (string, error) { return long, nil })
	calls := 0
	a := h.Agent[string]{Output: h.Text(), Capabilities: []h.Capability{h.Tools(lookup), c.PruneToolResults()}, Model: h.ModelFunc(func(_ context.Context, q h.Request) (h.Response, error) {
		calls++
		if calls == 1 {
			return h.Response{Calls: []h.Call{{Name: "lookup", ID: "result", Arguments: "{}"}}}, nil
		}
		if calls == 2 {
			return h.Response{Calls: []h.Call{{Name: "prune_tool_result", ID: "edit", Arguments: `{"tool_call_id":"result","summary":"needle"}`}, {Name: "lookup", ID: "wait", Arguments: "{}"}}}, nil
		}
		return h.Response{Text: "ok"}, nil
	})}
	r, err := a.Run(t.Context(), "go", h.Options{Authorize: func(_ context.Context, call h.Call) (h.Decision, error) {
		if call.ID == "wait" {
			return h.Defer, nil
		}
		return h.Approve, nil
	}})
	if !errors.Is(err, h.ErrDeferred) || r.History[2].Text != "[tool result pruned] needle" || r.History[2].CallID != "result" {
		t.Fatal(r, err)
	}
	r, err = a.Resume(t.Context(), r.Snapshot, h.Options{Authorize: func(context.Context, h.Call) (h.Decision, error) { return h.Approve, nil }})
	if err != nil || r.Output != "ok" || calls != 3 {
		t.Fatal(r, err)
	}
}

func TestModelSummaryConsumesSharedRequestLimit(t *testing.T) {
	helper := h.ModelFunc(func(_ context.Context, q h.Request) (h.Response, error) {
		if len(q.Tools) > 0 || q.Messages[0].Role != h.System {
			t.Fatal(q)
		}
		return h.Response{Text: "invoice 34", InputTokens: 39, OutputTokens: 4}, nil
	})
	p := c.CompactionPolicy{MaxMessages: 2, Strategies: []c.CompactionStrategy{c.WithReceipt(c.RetainRecent(1, c.ModelSummary(helper)), "saved-run")}}
	a := h.Agent[string]{Limits: h.Limits{Requests: 1}, Output: h.Text(), Capabilities: []h.Capability{p.Capability()}, Model: h.ModelFunc(func(context.Context, h.Request) (h.Response, error) {
		t.Fatal("main request exceeded budget")
		return h.Response{}, nil
	})}
	r, err := a.Run(t.Context(), "continue", h.Options{History: []h.Message{{Role: h.User, Text: strings.Repeat("long history ", 200)}, {Role: h.Assistant, Text: "previous"}}})
	if !errors.Is(err, h.ErrLimit) || r.Usage.Requests != 1 || r.Usage.InputTokens != 39 || !slices.ContainsFunc(r.History, func(m h.Message) bool {
		return m.ContextKind == "compaction_receipt" && strings.Contains(m.Text, "saved-run")
	}) {
		t.Fatal(r, err)
	}
}
func TestRelevancePreservesVerbatimExchange(t *testing.T) {
	var calls int
	judge := h.ModelFunc(func(_ context.Context, q h.Request) (h.Response, error) {
		calls++
		if strings.Contains(q.Messages[1].Text, "error-code") {
			return h.Response{Text: `{"needed_verbatim":1,"still_matters":1}`}, nil
		}
		return h.Response{Text: `{"needed_verbatim":0,"still_matters":0}`}, nil
	})
	history := []h.Message{{Role: h.User, Text: "fix"}, {Role: h.Assistant, Calls: []h.Call{{ID: "a", Name: "log"}}}, {Role: h.ToolResult, CallID: "a", Text: "error-code 413"}, {Role: h.Assistant, Calls: []h.Call{{ID: "b", Name: "log"}}}, {Role: h.ToolResult, CallID: "b", Text: strings.Repeat("noise ", 50)}, {Role: h.User, Text: "continue"}}
	s := c.RelevanceCompaction{Judge: judge, KeepTokens: 1, TargetTokens: 40}.Strategy()
	out, err := s(t.Context(), history, "")
	if err != nil || len(out) != 4 || out[2].CallID != "a" || out[2].Text != "error-code 413" || calls != 2 {
		t.Fatal(out, err, calls)
	}
}
