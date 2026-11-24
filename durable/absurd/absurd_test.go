package absurd_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"strings"
	"testing"

	sdk "github.com/earendil-works/absurd/sdks/go/absurd"
	_ "github.com/jackc/pgx/v5/stdlib"
	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/durable/absurd"
)

func TestAbsurdWorkerResumesAfterApproval(t *testing.T) {
	dsn := os.Getenv("GO_LLM_TEST_ABSURD")
	if dsn == "" {
		t.Skip("set GO_LLM_TEST_ABSURD to a test database with the Absurd schema")
	}
	queue := "harness_" + strings.ToLower(rand.Text())
	client, err := sdk.New(sdk.Options{DatabaseURL: dsn, DriverName: "pgx", QueueName: queue})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx := t.Context()
	if err := client.CreateQueue(ctx, queue); err != nil {
		t.Fatal(err)
	}
	var requests, runs int
	a := h.Agent[string]{Output: h.Text(), Model: h.ModelFunc(func(ctx context.Context, q h.Request) (h.Response, error) {
		requests++
		if last := q.Messages[len(q.Messages)-1]; last.Role == h.ToolResult {
			return h.Response{Text: last.Text}, nil
		}
		return h.Response{Calls: []h.Call{{ID: "c", Name: "work", Arguments: "{}"}}}, nil
	}), Capabilities: []h.Capability{h.Tools(h.NewTool("work", "", func(context.Context, struct{}) (string, error) {
		runs++
		return "done", nil
	}))}}
	options := absurd.Options{Authorize: func(context.Context, h.Call) (h.Decision, error) { return h.Defer, nil }, AwaitApproval: true}
	client.MustRegister(sdk.Task("agent", func(ctx context.Context, p string) (h.Snapshot, error) {
		return absurd.Run(ctx, a, h.Start(p, nil), options)
	}))
	id, err := client.Spawn(ctx, "agent", "do work")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.WorkBatch(ctx); err != nil {
		t.Fatal(err)
	}
	if requests != 1 || runs != 0 {
		t.Fatal(requests, runs)
	}
	if err := client.EmitEvent(ctx, queue, absurd.ApprovalEvent(id.TaskID, 1, h.Call{ID: "c"}), "approve"); err != nil {
		t.Fatal(err)
	}
	if err := client.WorkBatch(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := client.FetchTaskResult(ctx, queue, id.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	var s h.Snapshot
	if result == nil {
		t.Fatal("missing task result")
	}
	if err := json.Unmarshal(result.Result, &s); err != nil || !s.Complete || s.History[len(s.History)-1].Text != "done" || requests != 2 || runs != 1 {
		t.Fatal(result, requests, runs, err)
	}
}
