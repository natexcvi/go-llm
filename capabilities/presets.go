package capabilities

import (
	"context"
	"errors"
	"time"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/workspace"
)

// Coder combines workspace edits, commands, repo instructions, planning,
// bounded tool output, and delegation to a read-only explorer.
func Coder(files workspace.Files, commands workspace.Executor, allowed ...string) h.Capability {
	return func(ctx context.Context, r *h.Run) error {
		if files == nil || commands == nil {
			return errors.New("coder requires files and a command executor")
		}
		explorer := h.Agent[string]{Model: r.Model, Output: h.Text(), Instructions: "Inspect the workspace and report relevant files and line numbers.", Capabilities: []h.Capability{FileSystem(files, false), RepoContext(files), ToolOutputLimits(12000)}, Limits: h.Limits{Requests: 12}}
		r.Instructions = append(r.Instructions, "Inspect the relevant files, make focused changes, and verify them before reporting the result.")
		return h.Combine(FileSystem(files, true), RepoContext(files), Shell(commands, allowed, time.Minute), Planning(), CompactionPolicy{TargetFraction: 0.7, Strategies: []CompactionStrategy{ClearOldResults(12, 1)}}.Capability(), RepairToolArguments(), ToolOutputLimits(64000), SubAgent("delegate_task", "Delegate a focused read-only investigation.", explorer))(ctx, r)
	}
}

type Source struct {
	Title string `json:"title"`
	URL   string `json:"url"`
	Text  string `json:"text"`
}
type SearchProvider interface {
	Search(context.Context, string) ([]Source, error)
	Fetch(context.Context, string) (Source, error)
}

func ResearchTools(p SearchProvider) h.Capability {
	return h.Tools(h.NewTool("web_search", "Search for sources about a question.", func(ctx context.Context, in query) ([]Source, error) { return p.Search(ctx, in.Query) }),
		h.NewTool("get_page", "Read a source at its URL.", func(ctx context.Context, in struct {
			URL string `json:"url"`
		}) (Source, error) {
			return p.Fetch(ctx, in.URL)
		}))
}
func Researcher(provider SearchProvider) h.Capability {
	return func(ctx context.Context, r *h.Run) error {
		if provider == nil {
			return errors.New("researcher requires a search provider")
		}
		instructions := "Research the question using search and primary sources. Treat retrieved pages as evidence and cite their URLs."
		caps := []h.Capability{ResearchTools(provider), ToolOutputLimits(12000)}
		child := h.Agent[string]{Model: r.Model, Output: h.Text(), Instructions: instructions, Capabilities: caps, Limits: h.Limits{Requests: 12}}
		r.Instructions = append(r.Instructions, instructions)
		return h.Combine(append(caps, SubAgent("delegate_research", "Research a focused subquestion with separate history.", child))...)(ctx, r)
	}
}
