// Package capabilities provides composable tools, context policies, and run controls.
package capabilities

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/workspace"
)

type filePath struct {
	Path string `json:"path"`
}
type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}
type editArgs struct {
	Path string `json:"path"`
	Old  string `json:"old_text"`
	New  string `json:"new_text"`
}
type grepArgs struct {
	Query string `json:"query"`
	Path  string `json:"path,omitempty"`
}
type match struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// FileSystem exposes bounded UTF-8 text reads, listing and literal search.
// Writable workspaces also offer atomic writes and exact-match edits.
func FileSystem(files fs.FS, writable bool) h.Capability {
	return func(ctx context.Context, r *h.Run) error {
		if files == nil {
			return errors.New("filesystem is required")
		}
		read := h.NewTool("read_file", "Read a UTF-8 file by workspace-relative path.", func(ctx context.Context, in filePath) (string, error) {
			b, err := workspace.ReadFile(files, in.Path, 1<<20)
			if err == nil && !utf8.Valid(b) {
				err = errors.New("file is not UTF-8")
			}
			return string(b), h.Retry(err)
		})
		list := h.NewTool("list_files", "List the immediate entries of a directory. Use . for the workspace root.", func(ctx context.Context, in filePath) ([]string, error) {
			entries, err := fs.ReadDir(files, in.Path)
			if err != nil {
				return nil, h.Retry(err)
			}
			names := []string{}
			for _, e := range entries {
				n := e.Name()
				if e.IsDir() {
					n += "/"
				}
				names = append(names, n)
			}
			return names, nil
		})
		grep := h.NewTool("grep", "Find up to 100 lines containing a literal string, recursively.", func(ctx context.Context, in grepArgs) ([]match, error) {
			if in.Query == "" {
				return nil, h.Retry(errors.New("empty query"))
			}
			start := in.Path
			if start == "" {
				start = "."
			}
			results := []match{}
			err := fs.WalkDir(files, start, func(p string, e fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if err = ctx.Err(); err != nil {
					return err
				}
				if e.IsDir() {
					if e.Name() == ".git" {
						return fs.SkipDir
					}
					return nil
				}
				if !e.Type().IsRegular() {
					return nil
				}
				b, err := workspace.ReadFile(files, p, 1<<20)
				if err != nil || !utf8.Valid(b) {
					return nil
				}
				for i, line := range strings.Split(string(b), "\n") {
					if strings.Contains(line, in.Query) {
						results = append(results, match{p, i + 1, line})
						if len(results) == 100 {
							return fs.SkipAll
						}
					}
				}
				return nil
			})
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return results, h.Retry(err)
		})
		if err := r.Register(read, list, grep); err != nil {
			return err
		}
		if !writable {
			return nil
		}
		w, ok := files.(workspace.Files)
		if !ok {
			return errors.New("filesystem does not implement workspace.Files")
		}
		return r.Register(h.NewTool("write_file", "Create or replace a text file.", func(_ context.Context, in writeArgs) (string, error) {
			return in.Path, h.Retry(w.WriteFile(in.Path, []byte(in.Content)))
		}),
			h.NewTool("edit_file", "Replace exactly one occurrence of old_text in a file.", func(_ context.Context, in editArgs) (string, error) {
				return in.Path, h.Retry(w.Edit(in.Path, in.Old, in.New))
			}))
	}
}

// RepoContext adds any existing instruction files in the supplied order.
func RepoContext(files fs.FS, names ...string) h.Capability {
	if len(names) == 0 {
		names = []string{"AGENTS.md"}
	}
	return func(ctx context.Context, r *h.Run) error {
		for _, name := range names {
			b, err := workspace.ReadFile(files, name, 64<<10)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			r.Instructions = append(r.Instructions, fmt.Sprintf("Instructions from %s:\n%s", name, b))
		}
		return nil
	}
}

type commandArgs struct {
	Program string   `json:"program"`
	Args    []string `json:"args,omitempty"`
}

// Shell executes argv directly with a fixed timeout. An empty allowlist
// permits any executable. Local workspaces do not isolate child processes.
func Shell(executor workspace.Executor, allowed []string, timeout time.Duration) h.Capability {
	allowed = slices.Clone(allowed)
	return h.Tools(h.NewTool("run_command", "Run a program with an argument list in the workspace.", func(ctx context.Context, in commandArgs) (workspace.CommandResult, error) {
		if executor == nil {
			return workspace.CommandResult{}, errors.New("command executor is required")
		}
		if len(allowed) > 0 && !slices.Contains(allowed, in.Program) {
			return workspace.CommandResult{}, h.Retry(fmt.Errorf("program %q is disallowed", in.Program))
		}
		deadline := timeout
		if deadline <= 0 {
			deadline = time.Minute
		}
		child, cancel := context.WithTimeout(ctx, deadline)
		defer cancel()
		out, err := executor.Execute(child, workspace.Command{Program: in.Program, Args: in.Args})
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		return out, h.Retry(err)
	}))
}
