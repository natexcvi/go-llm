package pgtask_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/durable/pgtask"
)

func TestLeaseAndCheckpointReplay(t *testing.T) {
	dsn := os.Getenv("GO_LLM_TEST_PGTASK")
	if dsn == "" {
		t.Skip("set GO_LLM_TEST_PGTASK to a test database with the pgtask schema")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := t.Context()
	queue := "test-" + rand.Text()
	if _, err = db.ExecContext(ctx, "select pgtask.put_queue($1,604800,2592000,NULL,300)", queue); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, "select pgtask.enqueue('agent','{}',$1)", queue); err != nil {
		t.Fatal(err)
	}
	var lease pgtask.Lease
	err = db.QueryRowContext(ctx, `select id,lease_token,attempt,handler_version from pgtask.claim($1,gen_random_uuid(),array['agent'],array[1],1,60000)`, queue).Scan(&lease.TaskID, &lease.Token, &lease.Attempt, &lease.HandlerVersion)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	a := h.Agent[string]{Output: h.Text(), Model: h.ModelFunc(func(context.Context, h.Request) (h.Response, error) {
		n++
		return h.Response{Text: "ok"}, nil
	})}
	start := h.Start("run", nil)
	stale := lease
	stale.Attempt++
	if _, err = pgtask.Run(ctx, db, stale, a, start, nil); !errors.Is(err, pgtask.ErrLeaseLost) || n != 0 {
		t.Fatal(err, n)
	}
	for range 2 {
		s, err := pgtask.Run(ctx, db, lease, a, start, nil)
		if err != nil || !s.Complete || s.History[len(s.History)-1].Text != "ok" {
			t.Fatal(s, err)
		}
	}
	if n != 1 {
		t.Fatal(n)
	}
}
