package capabilities

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	h "github.com/natexcvi/go-llm"
)

// SpendLedger records costs in application-defined integer units. Add must
// atomically increment the amount for a scope and window.
type SpendLedger interface {
	Total(context.Context, string, time.Time) (int64, error)
	Add(context.Context, string, time.Time, int64) error
}
type spendKey struct {
	scope string
	start time.Time
}
type MemoryLedger struct {
	mu     sync.Mutex
	totals map[spendKey]int64
}

func (s *MemoryLedger) Total(ctx context.Context, k string, t time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.totals[spendKey{k, t}], ctx.Err()
}
func (s *MemoryLedger) Add(ctx context.Context, k string, t time.Time, n int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.totals == nil {
		s.totals = map[spendKey]int64{}
	}
	key := spendKey{k, t}
	if n < 0 || s.totals[key] > math.MaxInt64-n {
		return errors.New("invalid spend increment")
	}
	s.totals[key] += n
	return nil
}

type SpendBudget struct {
	Scope   string
	Ceiling int64
	// Window is measured in fixed UTC intervals; zero is a lifetime budget.
	Window time.Duration
	Ledger SpendLedger
	Price  func(h.Response) (int64, error)
	Clock  func() time.Time
}

// SpendLimits checks the ceiling before each request and charges the completed
// response. Concurrent in-flight requests may cross a shared ceiling.
func SpendLimits(b SpendBudget) h.Capability {
	return func(ctx context.Context, r *h.Run) error {
		if b.Scope == "" || b.Ceiling <= 0 || b.Window < 0 || b.Price == nil || b.Ledger == nil {
			return errors.New("invalid spend budget")
		}
		clock := b.Clock
		if clock == nil {
			clock = time.Now
		}
		var start time.Time
		r.BeforeRequest = append(r.BeforeRequest, func(ctx context.Context, _ *h.Request, _ h.Usage) error {
			if b.Window > 0 {
				start = clock().UTC().Truncate(b.Window)
			}
			n, err := b.Ledger.Total(ctx, b.Scope, start)
			if err != nil {
				return err
			}
			if n >= b.Ceiling {
				return fmt.Errorf("%w: spend ceiling for %s", h.ErrLimit, b.Scope)
			}
			return nil
		})
		r.AfterResponse = append(r.AfterResponse, func(ctx context.Context, resp h.Response) error {
			cost, err := b.Price(resp)
			if err != nil {
				return err
			}
			if cost < 0 {
				return errors.New("negative response price")
			}
			return b.Ledger.Add(ctx, b.Scope, start, cost)
		})
		return nil
	}
}
