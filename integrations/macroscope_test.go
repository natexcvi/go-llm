package integrations_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/integrations"
	"github.com/natexcvi/go-llm/workspace"
)

type executeFunc func(context.Context, workspace.Command) (workspace.CommandResult, error)

func (f executeFunc) Execute(ctx context.Context, c workspace.Command) (workspace.CommandResult, error) {
	return f(ctx, c)
}
func TestMacroscopeReview(t *testing.T) {
	cmd := executeFunc(func(_ context.Context, c workspace.Command) (workspace.CommandResult, error) {
		if c.Program != "macroscope" || !slices.Equal(c.Args, []string{"codereview", "--raw", "--base", "main"}) {
			t.Fatal(c)
		}
		return workspace.CommandResult{Output: "log review_id=abc\nissue_event=broken\n" + `issue_event={"issue_id":"a","sequence":1,"path":"main.go","severity":"low","category":"style","body":"see review_id=x"}` + "\nissue_status=completed"}, nil
	})
	a := h.Agent[string]{Output: h.Text(), Capabilities: []h.Capability{integrations.Macroscope{Executor: cmd, Base: "main"}.Capability()}, Model: h.ModelFunc(func(ctx context.Context, q h.Request) (h.Response, error) {
		last := q.Messages[len(q.Messages)-1]
		if last.Role == h.ToolResult {
			return h.Response{Text: last.Text}, nil
		}
		return h.Response{Calls: []h.Call{{ID: "review", Name: "run_macroscope_review", Arguments: "{}"}}}, nil
	})}
	out, err := a.Run(t.Context(), "review", h.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var review integrations.MacroscopeReview
	if err := json.Unmarshal([]byte(out.Output), &review); err != nil || review.Status != "completed" || review.ID != "abc" || len(review.Issues) != 1 {
		t.Fatal(review, err)
	}
}
