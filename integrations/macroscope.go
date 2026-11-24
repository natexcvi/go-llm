package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/workspace"
)

// Macroscope invokes the authenticated macroscope CLI in the supplied workspace.
type Macroscope struct {
	Executor      workspace.Executor
	Command, Base string
	Timeout       time.Duration
}
type MacroscopeIssue struct {
	ID       string `json:"issue_id"`
	Sequence int    `json:"sequence"`
	Path     string `json:"path"`
	Line     *int   `json:"line"`
	Severity string `json:"severity"`
	Category string `json:"category"`
	Body     string `json:"body"`
}
type MacroscopeReview struct {
	ID     string            `json:"review_id"`
	Status string            `json:"status"`
	Issues []MacroscopeIssue `json:"issues"`
}

func parseReview(text string) MacroscopeReview {
	result := MacroscopeReview{Status: "unknown", Issues: []MacroscopeIssue{}}
	for line := range strings.SplitSeq(text, "\n") {
		if _, data, ok := strings.Cut(line, "issue_event="); ok {
			var issue MacroscopeIssue
			if json.Unmarshal([]byte(data), &issue) == nil && issue.ID != "" && issue.Path != "" && issue.Severity != "" && issue.Category != "" && issue.Body != "" {
				result.Issues = append(result.Issues, issue)
			}
			continue
		}
		for _, field := range []struct {
			key  string
			dest *string
		}{{"issue_status=", &result.Status}, {"review_id=", &result.ID}} {
			if _, data, ok := strings.Cut(line, field.key); ok {
				tokens := strings.Fields(data)
				if len(tokens) > 0 {
					*field.dest = tokens[0]
				}
				break
			}
		}
	}
	return result
}
func (m Macroscope) Capability() h.Capability {
	return func(ctx context.Context, r *h.Run) error {
		bin := m.Command
		if bin == "" {
			bin = "macroscope"
		}
		if m.Executor == nil || strings.HasPrefix(bin, "-") || strings.Contains(bin, "=") || strings.HasPrefix(m.Base, "-") || m.Timeout < 0 {
			return errors.New("invalid Macroscope configuration")
		}
		timeout := m.Timeout
		if timeout == 0 {
			timeout = 10 * time.Minute
		}
		r.Instructions = append(r.Instructions, "Verify Macroscope review findings against the code before acting on them.")
		return r.Register(h.NewTool("run_macroscope_review", "Review the current branch with Macroscope.", func(ctx context.Context, in struct {
			Base *string `json:"base,omitempty"`
		}) (MacroscopeReview, error) {
			base := m.Base
			if in.Base != nil {
				base = *in.Base
			}
			if strings.HasPrefix(base, "-") {
				return MacroscopeReview{}, h.Retry(errors.New("base must be a git ref"))
			}
			args := []string{"codereview", "--raw"}
			if base != "" {
				args = append(args, "--base", base)
			}
			run, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			out, err := m.Executor.Execute(run, workspace.Command{Program: bin, Args: args})
			if ctx.Err() != nil {
				return MacroscopeReview{}, ctx.Err()
			}
			review := parseReview(out.Output)
			if errors.Is(run.Err(), context.DeadlineExceeded) {
				if review.ID == "" {
					return review, h.Retry(run.Err())
				}
				if review.Status == "unknown" {
					review.Status = "timed_out"
				}
				return review, nil
			}
			if err != nil {
				return review, err
			}
			if review.ID == "" {
				tail := []rune(out.Output)
				e := fmt.Errorf("Macroscope review did not start (exit %d): %s", out.ExitCode, string(tail[max(0, len(tail)-2000):]))
				if in.Base != nil {
					return review, h.Retry(e)
				}
				return review, e
			}
			if out.ExitCode != 0 && review.Status == "unknown" {
				review.Status = "interrupted"
			}
			return review, nil
		}))
	}
}
