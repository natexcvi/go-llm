package capabilities

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/workspace"
)

// ReportStaleFiles compares previously read files against the current workspace
// before each model request. The model receives one notice per observed change.
func ReportStaleFiles(files fs.FS) h.Capability {
	return func(ctx context.Context, r *h.Run) error {
		hashes := map[string]string{}
		if err := h.BindState(r, "file_hashes", &hashes); err != nil {
			return err
		}
		r.BeforeRequest = append(r.BeforeRequest, func(ctx context.Context, q *h.Request, _ h.Usage) error {
			var changed []string
			for _, name := range slices.Sorted(maps.Keys(hashes)) {
				if err := ctx.Err(); err != nil {
					return err
				}
				b, err := workspace.ReadFile(files, name, 1<<20)
				if errors.Is(err, fs.ErrNotExist) {
					changed = append(changed, name+" (deleted)")
					delete(hashes, name)
					continue
				}
				if err != nil {
					return err
				}
				sum := fmt.Sprintf("%x", sha256.Sum256(b))
				if hashes[name] != sum {
					changed = append(changed, name)
					hashes[name] = sum
				}
			}
			if len(changed) > 0 {
				q.Messages = append(q.Messages, h.Message{Role: h.User, Text: "Previously read workspace files changed: " + strings.Join(changed, ", ") + ". Read them again before editing."})
			}
			return nil
		})
		return wrap(func(next h.ToolHandler) h.ToolHandler {
			return func(ctx context.Context, c h.Call) (string, error) {
				text, err := next(ctx, c)
				if err == nil && c.Name == "read_file" {
					var in filePath
					if json.Unmarshal([]byte(c.Arguments), &in) == nil {
						hashes[in.Path] = fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
					}
				}
				return text, err
			}
		})(ctx, r)
	}
}

// ReportToolErrors delivers the call and error to application telemetry while
// preserving the original error and retry behavior.
func ReportToolErrors(report func(context.Context, h.Call, error)) h.Capability {
	return wrap(func(next h.ToolHandler) h.ToolHandler {
		return func(ctx context.Context, c h.Call) (string, error) {
			s, err := next(ctx, c)
			if err != nil {
				report(ctx, c, err)
			}
			return s, err
		}
	})
}

type TrajectoryDecision struct {
	Steer string
	Stop  bool
}

// TrajectoryJudge reviews the model-facing transcript at a request cadence.
func TrajectoryJudge(every int, judge func(context.Context, []h.Message) (TrajectoryDecision, error)) h.Capability {
	return before(func(ctx context.Context, q *h.Request, u h.Usage) error {
		if every < 1 {
			return errors.New("trajectory review cadence must be positive")
		}
		if u.Requests == 0 || u.Requests%every != 0 {
			return nil
		}
		v, err := judge(ctx, q.Messages)
		if err != nil {
			return err
		}
		if v.Stop {
			return fmt.Errorf("trajectory judge stopped run: %s", v.Steer)
		}
		if v.Steer != "" {
			q.Messages = append(q.Messages, h.Message{Role: h.User, Text: "Feedback from the trajectory reviewer:\n" + v.Steer})
		}
		return nil
	})
}
