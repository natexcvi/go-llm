package storage

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"math"
	"path"
	"slices"
	"strings"
)

// BoundedStore lets notebook tools bound data fetched from the backend. The
// bool return from GetPrefix reports whether more bytes are available.
type BoundedStore interface {
	Store
	GetPrefix(context.Context, string, int) ([]byte, bool, error)
	ListFirst(context.Context, int) ([]string, error)
}

func boundsOK(limit int) bool { return limit > 0 && limit < math.MaxInt }
func (m *Memory) GetPrefix(ctx context.Context, key string, limit int) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if !valid(key) || !boundsOK(limit) {
		return nil, false, fs.ErrInvalid
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.values[key]
	if !ok {
		return nil, false, fs.ErrNotExist
	}
	return slices.Clone(b[:min(len(b), limit)]), len(b) > limit, nil
}
func (m *Memory) ListFirst(ctx context.Context, limit int) ([]string, error) {
	if !boundsOK(limit) {
		return nil, fs.ErrInvalid
	}
	keys, err := m.List(ctx)
	return keys[:min(limit, len(keys))], err
}
func (s *Directory) GetPrefix(ctx context.Context, key string, limit int) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if !valid(key) || !boundsOK(limit) {
		return nil, false, fs.ErrInvalid
	}
	f, err := s.root.Open(key)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, fs.ErrInvalid
	}
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	return b[:min(len(b), limit)], len(b) > limit, err
}
func (s *Directory) ListFirst(ctx context.Context, limit int) ([]string, error) {
	if !boundsOK(limit) {
		return nil, fs.ErrInvalid
	}
	return s.list(ctx, limit)
}
func (s *Directory) list(ctx context.Context, limit int) ([]string, error) {
	keys := []string{}
	err := fs.WalkDir(s.root.FS(), ".", func(key string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.Type().IsRegular() && !strings.HasPrefix(path.Base(key), ".go-llm-") {
			keys = append(keys, key)
			if len(keys) == limit {
				return fs.SkipAll
			}
		}
		return nil
	})
	return keys, err
}
func (s *SQL) GetPrefix(ctx context.Context, key string, limit int) ([]byte, bool, error) {
	if !valid(key) || !boundsOK(limit) {
		return nil, false, fs.ErrInvalid
	}
	var b []byte
	err := s.db.QueryRowContext(ctx, "WITH document AS (SELECT body FROM go_llm_documents WHERE scope=$1 AND name=$2) SELECT substr(body,1,$3) FROM document", s.scope, key, limit+1).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		err = fs.ErrNotExist
	}
	return b[:min(len(b), limit)], len(b) > limit, err
}
func (s *SQL) ListFirst(ctx context.Context, limit int) ([]string, error) {
	if !boundsOK(limit) {
		return nil, fs.ErrInvalid
	}
	return s.list(ctx, "SELECT name FROM go_llm_documents WHERE scope=$1 ORDER BY name LIMIT $2", s.scope, limit)
}
