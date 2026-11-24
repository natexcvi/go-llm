package capabilities

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"

	h "github.com/natexcvi/go-llm"
)

// Jobs manages background work for one application scope. The owner cancels
// the supplied lifetime context on shutdown and calls Close to join workers.
type Jobs struct {
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	workers sync.WaitGroup
	jobs    map[string]*job
	max     int
	closed  bool
}
type job struct {
	tool   string
	cancel context.CancelFunc
	result JobResult
}
type JobResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

func NewJobs(lifetime context.Context, maxJobs int) *Jobs {
	if maxJobs < 1 {
		maxJobs = 16
	}
	ctx, cancel := context.WithCancel(lifetime)
	return &Jobs{ctx: ctx, cancel: cancel, jobs: map[string]*job{}, max: maxJobs}
}
func (j *Jobs) Close() {
	j.mu.Lock()
	j.closed = true
	j.cancel()
	j.mu.Unlock()
	j.workers.Wait()
}
func (j *Jobs) start(name string, fn func(context.Context) (string, error)) (JobResult, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed || j.ctx.Err() != nil {
		return JobResult{}, errors.New("job manager closed")
	}
	if len(j.jobs) >= j.max {
		return JobResult{}, h.Retry(errors.New("job limit reached; collect completed jobs"))
	}
	ctx, cancel := context.WithCancel(j.ctx)
	id := rand.Text()
	x := &job{tool: name, cancel: cancel, result: JobResult{ID: id, Status: "running"}}
	j.jobs[id] = x
	initial := x.result
	j.workers.Add(1)
	go func() {
		defer j.workers.Done()
		defer cancel()
		out, err := fn(ctx)
		j.mu.Lock()
		defer j.mu.Unlock()
		x.result.Status = "completed"
		x.result.Output = out
		if err != nil {
			x.result.Status = "failed"
			x.result.Error = err.Error()
		}
		if ctx.Err() != nil {
			x.result.Status = "cancelled"
		}
	}()
	return initial, nil
}

// BackgroundTool exposes start, check and stop tools for an explicitly supplied
// handler. Completed results are consumed when checked.
func BackgroundTool(j *Jobs, tool h.Tool) h.Capability {
	return func(ctx context.Context, r *h.Run) error {
		if j == nil || tool.Execute == nil {
			return errors.New("background tool requires jobs and a handler")
		}
		run := tool
		run.Name = "start_" + tool.Name
		run.Execute = func(ctx context.Context, c h.Call) (string, error) {
			c.Name = tool.Name
			if r.Authorize != nil {
				decision, err := r.Authorize(ctx, c)
				if err != nil {
					return "", err
				}
				switch decision {
				case h.Approve:
				case h.Defer:
					return "", fmt.Errorf("%w: %s", h.ErrDeferred, c.ID)
				case h.Deny:
					return "Background tool denied by application policy.", nil
				default:
					return "", errors.New("invalid background tool authorization")
				}
			}
			out, err := j.start(tool.Name, func(child context.Context) (string, error) { return tool.Execute(child, c) })
			return out.ID, err
		}
		type idArg struct {
			ID string `json:"id"`
		}
		inspect := func(stop bool) func(context.Context, idArg) (JobResult, error) {
			return func(_ context.Context, in idArg) (JobResult, error) {
				j.mu.Lock()
				defer j.mu.Unlock()
				x, ok := j.jobs[in.ID]
				if !ok || x.tool != tool.Name {
					return JobResult{}, h.Retry(errors.New("unknown job"))
				}
				if stop {
					x.cancel()
				} else if x.result.Status != "running" {
					delete(j.jobs, in.ID)
				}
				return x.result, nil
			}
		}
		return r.Register(run, h.NewTool("check_"+tool.Name, "Check a background job and collect its result.", inspect(false)), h.NewTool("stop_"+tool.Name, "Request cancellation of a background job.", inspect(true)))
	}
}
