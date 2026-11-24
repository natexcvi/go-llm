// Package storage saves named JSON checkpoints and agent memory documents.
package storage

import (
	"context"
	"io/fs"
	"os"
	"slices"
	"sort"
	"sync"

	"github.com/natexcvi/go-llm/workspace"
)

type Store interface {
	Get(context.Context, string) ([]byte, error)
	Put(context.Context, string, []byte) error
	Delete(context.Context, string) error
	List(context.Context) ([]string, error)
}

func valid(key string) bool { return key != "." && fs.ValidPath(key) }

type Memory struct {
	mu     sync.RWMutex
	values map[string][]byte
}

func (m *Memory) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !valid(key) {
		return nil, fs.ErrInvalid
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.values[key]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return slices.Clone(b), nil
}
func (m *Memory) Put(ctx context.Context, key string, b []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !valid(key) {
		return fs.ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.values == nil {
		m.values = map[string][]byte{}
	}
	m.values[key] = slices.Clone(b)
	return nil
}
func (m *Memory) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !valid(key) {
		return fs.ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.values[key]; !ok {
		return fs.ErrNotExist
	}
	delete(m.values, key)
	return nil
}
func (m *Memory) List(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := []string{}
	for k := range m.values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

type Directory struct{ root *os.Root }

func Open(dir string) (*Directory, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &Directory{root}, nil
}
func (s *Directory) Close() error { return s.root.Close() }
func (s *Directory) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !valid(key) {
		return nil, fs.ErrInvalid
	}
	return s.root.ReadFile(key)
}
func (s *Directory) Put(ctx context.Context, key string, b []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return workspace.AtomicWrite(s.root, key, b)
}
func (s *Directory) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !valid(key) {
		return fs.ErrInvalid
	}
	info, err := s.root.Lstat(key)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fs.ErrInvalid
	}
	return s.root.Remove(key)
}
func (s *Directory) List(ctx context.Context) ([]string, error) {
	return s.list(ctx, 0)
}

var _ Store = (*Directory)(nil)
var _ Store = (*Memory)(nil)
