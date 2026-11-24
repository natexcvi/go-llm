// Package pgtask journals agent steps with the pgtask PostgreSQL protocol.
// The host worker claims the task, renews its lease, and reports completion.
package pgtask

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/durable"
)

// Lease identifies a task already claimed by the host worker.
type Lease struct {
	TaskID, Token           string
	Attempt, HandlerVersion int
}

var ErrLeaseLost = errors.New("pgtask lease is no longer active")

// Run returns stored results for completed steps and executes the next missing
// transition. Use stable agent configuration within a handler version. Incomplete
// external side effects still need idempotency under process failure.
func Run[T any](ctx context.Context, db *sql.DB, lease Lease, a h.Agent[T], s h.Snapshot, policy func(context.Context, h.Call) (h.Decision, error)) (h.Snapshot, error) {
	if db == nil || lease.TaskID == "" || lease.Token == "" || lease.Attempt < 1 || lease.HandlerVersion < 1 {
		return s, errors.New("database and claimed pgtask lease required")
	}
	for n := 0; !s.Complete; n++ {
		name := fmt.Sprintf("agent-step-%d", n)
		var raw []byte
		err := db.QueryRowContext(ctx, "SELECT value FROM pgtask.get_checkpoint($1,$2,$3,1)", lease.TaskID, lease.HandlerVersion, name).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			var active bool
			err = db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pgtask.get_task($1)
				WHERE state='running' AND attempt=$2 AND lease_token=$3 AND handler_version=$4 AND lease_expires_at>now())`, lease.TaskID, lease.Attempt, lease.Token, lease.HandlerVersion).Scan(&active)
			if err != nil {
				return s, err
			}
			if !active {
				return s, ErrLeaseLost
			}
			next, err := durable.Advance(ctx, a, durable.Input{Snapshot: s}, policy)
			if err != nil {
				return s, err
			}
			if next.Paused {
				return next.Snapshot, h.ErrDeferred
			}
			value, err := json.Marshal(next.Snapshot)
			if err != nil {
				return s, err
			}
			err = db.QueryRowContext(ctx, "SELECT value FROM pgtask.commit_checkpoint($1,$2,$3,$4,1,$5::jsonb)", lease.TaskID, lease.Attempt, lease.Token, name, string(value)).Scan(&raw)
			if errors.Is(err, sql.ErrNoRows) {
				return s, ErrLeaseLost
			}
			if err != nil {
				return s, err
			}
		} else if err != nil {
			return s, err
		}
		var next h.Snapshot
		if err := json.Unmarshal(raw, &next); err != nil {
			return s, fmt.Errorf("pgtask checkpoint %s: %w", name, err)
		}
		if len(next.History) == 0 || next.Usage.Requests < 1 {
			return s, errors.New("invalid pgtask agent checkpoint")
		}
		s = next
	}
	return s, nil
}
