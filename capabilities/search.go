package capabilities

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/storage"
	"github.com/natexcvi/go-llm/workspace"
)

type Excerpt struct {
	Source string  `json:"source"`
	Start  int     `json:"start"`
	End    int     `json:"end"`
	Text   string  `json:"text"`
	Score  float64 `json:"score"`
}

func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(c rune) bool { return !unicode.IsLetter(c) && !unicode.IsDigit(c) })
}

// rank uses BM25. Stable sorting preserves source order for tied scores.
func rank(q string, docs []Excerpt, maxResults int) []Excerpt {
	terms := map[string]bool{}
	for _, w := range words(q) {
		terms[w] = true
	}
	if len(terms) == 0 || len(docs) == 0 {
		return []Excerpt{}
	}
	counts := make([]map[string]int, len(docs))
	df := map[string]int{}
	lengths := make([]int, len(docs))
	total := 0
	for i, e := range docs {
		w := words(e.Source + " " + e.Text)
		lengths[i] = len(w)
		total += len(w)
		counts[i] = map[string]int{}
		for _, term := range w {
			if terms[term] {
				if counts[i][term] == 0 {
					df[term]++
				}
				counts[i][term]++
			}
		}
	}
	avg := float64(max(1, total)) / float64(len(docs))
	found := []Excerpt{}
	// Sorted query terms give reproducible scores independent of map iteration order.
	ordered := make([]string, 0, len(terms))
	for term := range terms {
		ordered = append(ordered, term)
	}
	sort.Strings(ordered)
	for i, e := range docs {
		for _, term := range ordered {
			freq := float64(counts[i][term])
			if freq == 0 {
				continue
			}
			idf := math.Log1p((float64(len(docs)-df[term]) + .5) / (float64(df[term]) + .5))
			e.Score += idf * freq * 2.2 / (freq + 1.2*(.25+.75*float64(lengths[i])/avg))
		}
		if e.Score > 0 {
			found = append(found, e)
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].Score > found[j].Score })
	return found[:min(len(found), maxResults)]
}

// ConversationSearch searches a store scoped by the application to permitted
// conversation checkpoints. Positions refer to one-based transcript messages.
func ConversationSearch(store storage.Store) h.Capability {
	return h.Tools(h.NewTool("search_conversation_history", "Find ranked excerpts from saved conversations.", func(ctx context.Context, in query) ([]Excerpt, error) {
		keys, err := store.List(ctx)
		if err != nil {
			return nil, err
		}
		var docs []Excerpt
		for _, key := range keys {
			s, err := storage.Load(ctx, store, key)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			for i, m := range s.History {
				if m.Role != h.System && m.Text != "" {
					docs = append(docs, Excerpt{Source: key, Start: i + 1, End: i + 1, Text: clamp(m.Text, 3000)})
				}
			}
		}
		return rank(in.Query, docs, 10), nil
	}))
}

// SmartSearch ranks source chunks lexically and optionally asks a relevance
// judge to score the shortlist. A judge score of zero drops a chunk.
func SmartSearch(files fs.FS, judge func(context.Context, string, Excerpt) (float64, error)) h.Capability {
	return h.Tools(h.NewTool("smart_search", "Rank source excerpts by relevance to a description.", func(ctx context.Context, in struct {
		Query     string `json:"query"`
		Directory string `json:"directory,omitempty"`
	}) ([]Excerpt, error) {
		dir := in.Directory
		if dir == "" {
			dir = "."
		}
		var docs []Excerpt
		size := 0
		err := fs.WalkDir(files, dir, func(name string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err = ctx.Err(); err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", "node_modules", "vendor":
					return fs.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			b, err := workspace.ReadFile(files, name, 256<<10)
			if err != nil || !utf8.Valid(b) || strings.IndexByte(string(b), 0) >= 0 {
				return nil
			}
			size += len(b)
			if size > 32<<20 {
				return h.Retry(errors.New("source search exceeds 32 MiB; narrow the directory"))
			}
			lines := strings.Split(string(b), "\n")
			for i := 0; i < len(lines); i += 40 {
				end := min(len(lines), i+40)
				docs = append(docs, Excerpt{Source: name, Start: i + 1, End: end, Text: strings.Join(lines[i:end], "\n")})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		top := rank(in.Query, docs, 12)
		if judge != nil {
			selected := top[:0]
			for _, e := range top {
				score, err := judge(ctx, in.Query, e)
				if err != nil {
					return nil, err
				}
				if score > 0 {
					e.Score = score
					selected = append(selected, e)
				}
			}
			top = selected
			sort.SliceStable(top, func(i, j int) bool { return top[i].Score > top[j].Score })
		}
		return top, nil
	}))
}
