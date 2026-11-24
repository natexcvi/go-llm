package storage

import (
	"context"
	"encoding/json"

	h "github.com/natexcvi/go-llm"
)

// Checkpoint returns an Options.Checkpoint callback. Use a distinct key per run.
func Checkpoint(s Store, key string) func(context.Context, h.Snapshot) error {
	return func(ctx context.Context, snap h.Snapshot) error {
		b, err := json.Marshal(snap)
		if err != nil {
			return err
		}
		return s.Put(ctx, key, b)
	}
}
func Load(ctx context.Context, s Store, key string) (h.Snapshot, error) {
	var snap h.Snapshot
	b, err := s.Get(ctx, key)
	if err == nil {
		err = json.Unmarshal(b, &snap)
	}
	return snap, err
}
