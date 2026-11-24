package lambda_test

import (
	"context"
	"testing"

	"github.com/aws/aws-durable-execution-sdk-go/durable"
	"github.com/aws/aws-durable-execution-sdk-go/durable/durabletest"
	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/durable/lambda"
)

func TestLambdaReplaysCompletedStepsAfterApproval(t *testing.T) {
	requests, executions := 0, 0
	a := h.Agent[string]{Output: h.Text(), Model: h.ModelFunc(func(ctx context.Context, q h.Request) (h.Response, error) {
		requests++
		last := q.Messages[len(q.Messages)-1]
		if last.Role == h.ToolResult {
			return h.Response{Text: last.Text}, nil
		}
		return h.Response{Calls: []h.Call{{Name: "record", ID: "one", Arguments: "{}"}, {Name: "record", ID: "two", Arguments: "{}"}}}, nil
	}), Capabilities: []h.Capability{h.Tools(h.NewTool("record", "", func(context.Context, struct{}) (string, error) {
		executions++
		return "done", nil
	}))}}
	opts := lambda.Options{Authorize: func(_ context.Context, c h.Call) (h.Decision, error) {
		if c.ID == "two" {
			return h.Defer, nil
		}
		return h.Approve, nil
	}, Submit: func(_ context.Context, c h.Call, id string) error {
		if c.ID != "two" || id == "" {
			t.Errorf("unexpected approval %v %q", c, id)
		}
		return nil
	}}
	runner := durabletest.NewLocalRunner(func(ctx durable.Context, in string) (string, error) {
		result, err := lambda.Run(ctx, a, h.Start(in, nil), opts)
		return result.Output, err
	})
	result, err := runner.RunUntilComplete("begin")
	if err != nil || result.Status != durabletest.Pending || requests != 1 || executions != 1 {
		t.Fatal(result, requests, executions, err)
	}
	callbacks := runner.OpenCallbacks()
	if len(callbacks) != 1 {
		t.Fatal(callbacks)
	}
	if err := runner.SendCallbackSuccess(callbacks[0].CallbackID, "approve"); err != nil {
		t.Fatal(err)
	}
	result, err = runner.RunUntilComplete("begin")
	if err != nil || result.Status != durabletest.Succeeded {
		t.Fatal(result.Error, err)
	}
	answer, err := durabletest.ResultAs[string](result)
	if err != nil || answer != "done" || requests != 2 || executions != 2 {
		t.Fatal(answer, requests, executions, err)
	}
}
