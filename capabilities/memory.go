package capabilities

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/storage"
)

type memoryFile struct {
	File string `json:"file"`
}
type memoryWrite struct {
	File    string `json:"file"`
	Content string `json:"content"`
}
type query struct {
	Query string `json:"query"`
}

// Memory exposes a caller-scoped store across runs. Give each tenant a distinct store.
func Memory(s storage.Store, checks ...ArtifactCheck) h.Capability {
	return Notebook(s, NotebookOptions{}, checks...)
}

// NotebookOptions bounds the model-visible snapshot, reads, and search work.
// The zero value uses MEMORY.md and per-run snapshots. Scope distinguishes
// unrelated notebooks when the application reuses conversation histories.
type NotebookOptions struct {
	MainFile, Scope                                      string
	SnapshotChars, ReadChars, SearchFiles, SearchResults int
	DisableSnapshot                                      bool
}

func Notebook(s storage.Store, opts NotebookOptions, checks ...ArtifactCheck) h.Capability {
	if opts.MainFile == "" {
		opts.MainFile = "MEMORY.md"
	}
	if opts.SnapshotChars == 0 {
		opts.SnapshotChars = 8000
	}
	if opts.ReadChars == 0 {
		opts.ReadChars = 32000
	}
	if opts.SearchFiles == 0 {
		opts.SearchFiles = 100
	}
	if opts.SearchResults == 0 {
		opts.SearchResults = 20
	}
	checks = slices.Clone(checks)
	return func(ctx context.Context, r *h.Run) error {
		if s == nil || !fs.ValidPath(opts.MainFile) || opts.MainFile == "." || opts.SnapshotChars < 1 || opts.ReadChars < 1 || opts.SearchFiles < 1 || opts.SearchResults < 1 || opts.ReadChars >= math.MaxInt/4 || opts.SnapshotChars >= math.MaxInt/4 {
			return errors.New("invalid notebook configuration")
		}
		if !opts.DisableSnapshot {
			kind := "memory:" + opts.Scope
			r.RewriteHistory = append(r.RewriteHistory, func(ctx context.Context, q h.Request, _ h.Usage) ([]h.Message, error) {
				n := len(q.Messages)
				if n == 0 || !r.FirstRequest || q.Messages[n-1].Role != h.User || q.Messages[n-1].Text != r.Prompt {
					return nil, nil
				}
				text, err := notebookExcerpt(ctx, s, opts, checks)
				if err != nil {
					return nil, err
				}
				if text == "" && n == 1 {
					return nil, nil
				}
				if text == "" {
					text = "The notebook is empty."
				}
				text = "<memory>Notes saved by the agent in earlier runs.\n" + clamp(html.EscapeString(text), opts.SnapshotChars) + "\n</memory>"
				for i := n - 1; i >= 0; i-- {
					if q.Messages[i].ContextKind == kind {
						if q.Messages[i].Text == text {
							return nil, nil
						}
						break
					}
				}
				return slices.Insert(q.Messages, n-1, h.Message{Role: h.User, Text: text, ContextKind: kind}), nil
			})
		}
		return r.Register(
			h.NewTool("write_memory", "Save a text memory file.", func(ctx context.Context, in memoryWrite) (string, error) {
				if err := checkArtifact(checks, in.File, in.Content); err != nil {
					return "", h.Retry(err)
				}
				return in.File, memoryErr(s.Put(ctx, in.File, []byte(in.Content)))
			}),
			h.NewTool("read_memory", "Read a saved memory file.", func(ctx context.Context, in memoryFile) (string, error) {
				text, more, e := notebookRead(ctx, s, in.File, opts.ReadChars)
				if e == nil {
					e = h.Retry(checkArtifact(checks, in.File, text))
				}
				if e != nil {
					return "", memoryErr(e)
				}
				if more {
					text += "\n[truncated]"
				}
				return text, nil
			}),
			h.NewTool("delete_memory", "Delete a saved memory file.", func(ctx context.Context, in memoryFile) (string, error) {
				if in.File == opts.MainFile {
					return "", h.Retry(errors.New("main notebook cannot be deleted"))
				}
				if err := checkArtifact(checks, in.File, ""); err != nil {
					return "", h.Retry(err)
				}
				return in.File, memoryErr(s.Delete(ctx, in.File))
			}),
			h.NewTool("search_memory", "Find saved files containing text, ignoring case.", func(ctx context.Context, in query) ([]string, error) {
				keys, err := notebookList(ctx, s, opts.SearchFiles)
				if err != nil {
					return nil, err
				}
				matches := []string{}
				slices.Sort(keys)
				needle := strings.ToLower(in.Query)
				for _, key := range keys[:min(len(keys), opts.SearchFiles)] {
					text, _, err := notebookRead(ctx, s, key, opts.ReadChars)
					if errors.Is(err, fs.ErrNotExist) {
						continue
					}
					if err != nil {
						return nil, err
					}
					if checkArtifact(checks, key, text) != nil {
						continue
					}
					if strings.Contains(strings.ToLower(text), needle) {
						matches = append(matches, key)
						if len(matches) >= opts.SearchResults {
							break
						}
					}
				}
				return matches, nil
			}),
		)
	}
}
func notebookExcerpt(ctx context.Context, s storage.Store, opts NotebookOptions, checks []ArtifactCheck) (string, error) {
	keys, err := notebookList(ctx, s, opts.SearchFiles)
	if err != nil {
		return "", err
	}
	slices.Sort(keys)
	var text strings.Builder
	main, more, err := notebookRead(ctx, s, opts.MainFile, opts.SnapshotChars)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if err == nil && checkArtifact(checks, opts.MainFile, main) == nil {
		fmt.Fprintf(&text, "%s: %s", opts.MainFile, main)
		if more {
			text.WriteString("\n[truncated]")
		}
	}
	remaining := opts.SnapshotChars - utf8.RuneCountInString(text.String())
	for _, key := range keys {
		if key == opts.MainFile || checkArtifact(checks, key, "") != nil {
			continue
		}
		size := 1 + utf8.RuneCountInString(key)
		if size > remaining {
			break
		}
		text.WriteByte('\n')
		text.WriteString(key)
		remaining -= size
	}
	return strings.TrimSpace(clamp(text.String(), opts.SnapshotChars)), nil
}

func memoryErr(err error) error {
	if errors.Is(err, fs.ErrInvalid) || errors.Is(err, fs.ErrNotExist) {
		return h.Retry(err)
	}
	return err
}

func notebookList(ctx context.Context, s storage.Store, limit int) ([]string, error) {
	if b, ok := s.(storage.BoundedStore); ok {
		return b.ListFirst(ctx, limit)
	}
	keys, err := s.List(ctx)
	slices.Sort(keys)
	return keys[:min(len(keys), limit)], err
}
func notebookRead(ctx context.Context, s storage.Store, key string, chars int) (string, bool, error) {
	var b []byte
	var more bool
	var err error
	if bounded, ok := s.(storage.BoundedStore); ok {
		b, more, err = bounded.GetPrefix(ctx, key, 4*chars)
	} else {
		b, err = s.Get(ctx, key)
	}
	if err != nil {
		return "", false, err
	}
	n := 0
	for i := range string(b) {
		if n == chars {
			return string(b[:i]), true, nil
		}
		n++
	}
	return string(b), more, nil
}
