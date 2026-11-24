package azure_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/backend"
	"github.com/microsoft/durabletask-go/backend/postgres"
	"github.com/microsoft/durabletask-go/task"
	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/durable/azure"
)

func TestAzureWorkerHandlesApprovalEvent(t *testing.T) {
	dsn := os.Getenv("GO_LLM_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("set GO_LLM_TEST_POSTGRES to a dedicated test database")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	registry := task.NewTaskRegistry()
	a := h.Agent[string]{Output: h.Text(), Model: h.ModelFunc(func(ctx context.Context, q h.Request) (h.Response, error) {
		last := q.Messages[len(q.Messages)-1]
		if last.Role == h.ToolResult {
			return h.Response{Text: last.Text}, nil
		}
		return h.Response{Calls: []h.Call{{ID: "c", Name: "record", Arguments: "{}"}}}, nil
	}), Capabilities: []h.Capability{h.Tools(h.NewTool("record", "", func(context.Context, struct{}) (string, error) { return "recorded", nil }))}}
	opts := azure.Options{Authorize: func(context.Context, h.Call) (h.Decision, error) { return h.Defer, nil }, ApprovalTimeout: time.Minute}
	if err := azure.Register(registry, "run", a, opts); err != nil {
		t.Fatal(err)
	}
	logger := backend.DefaultLogger()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	be := postgres.NewPostgresBackend(&postgres.PostgresOptions{PgOptions: cfg, OrchestrationLockTimeout: time.Minute, ActivityLockTimeout: time.Minute}, logger)
	executor := task.NewTaskExecutor(registry)
	worker := backend.NewTaskHubWorker(be, backend.NewOrchestrationWorker(be, executor, logger), backend.NewActivityTaskWorker(be, executor, logger), logger)
	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer worker.Shutdown(ctx)
	client := backend.NewTaskHubClient(be)
	id, err := client.ScheduleNewOrchestration(ctx, "run", api.WithInput(h.Start("begin", nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err = client.RaiseEvent(ctx, id, azure.ApprovalEvent(1, h.Call{ID: "c"}), api.WithEventPayload("approve")); err != nil {
		t.Fatal(err)
	}
	state, err := client.WaitForOrchestrationCompletion(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	var out h.Snapshot
	if err = json.Unmarshal([]byte(state.SerializedOutput), &out); err != nil || !out.Complete || out.Usage.ToolCalls != 1 || out.History[len(out.History)-1].Text != "recorded" {
		t.Fatal(state, err)
	}
}
