// Package workspace provides the file and command backends used by capabilities.
package workspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Files interface {
	fs.FS
	WriteFile(name string, data []byte) error
	// Edit replaces exactly one occurrence, or leaves the file intact on error.
	Edit(name, oldText, newText string) error
}
type Command struct {
	Program string
	Args    []string
}
type CommandResult struct {
	Output    string `json:"output"`
	ExitCode  int    `json:"exit_code"`
	Truncated bool   `json:"truncated,omitempty"`
}
type Executor interface {
	Execute(context.Context, Command) (CommandResult, error)
}

// Local confines file access with os.Root. Processes run with the host user's
// privileges. The caller owns the workspace and closes it after all runs finish.
type Local struct {
	root *os.Root
	mu   sync.Mutex
	dir  string
	env  []string
}

// Open creates a handle to an existing directory. Processes inherit only the
// environment supplied here. Use absolute executable paths for pinned commands.
func Open(dir string, env []string) (*Local, error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, err
	}
	return &Local{root: root, dir: absolute, env: append([]string{}, env...)}, nil
}
func (l *Local) Close() error { return l.root.Close() }
func (l *Local) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, fs.ErrInvalid
	}
	return l.root.Open(name)
}
func (l *Local) WriteFile(name string, data []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return AtomicWrite(l.root, name, data)
}

// AtomicWrite replaces a workspace file using a same-directory rename.
// Existing regular files retain their permissions. New files are owner-only.
func AtomicWrite(root *os.Root, name string, data []byte) error {
	if !fs.ValidPath(name) || name == "." {
		return fs.ErrInvalid
	}
	mode := fs.FileMode(0600)
	info, err := root.Lstat(name)
	switch {
	case err == nil:
		if !info.Mode().IsRegular() {
			return fmt.Errorf("expected regular file: %s", name)
		}
		mode = info.Mode().Perm()
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	dir := path.Dir(name)
	if err := root.MkdirAll(dir, 0700); err != nil {
		return err
	}
	var suffix [16]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	tmp := path.Join(dir, ".go-llm-"+hex.EncodeToString(suffix[:]))
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(mode)
	}
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	return root.Rename(tmp, name)
}
func (l *Local) Edit(name, oldText, newText string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if oldText == "" {
		return errors.New("old text is empty")
	}
	data, err := ReadFile(l, name, 8<<20)
	if err != nil {
		return err
	}
	if strings.Count(string(data), oldText) != 1 {
		return errors.New("old text must occur exactly once")
	}
	return AtomicWrite(l.root, name, []byte(strings.Replace(string(data), oldText, newText, 1)))
}

// ReadFile rejects special files and detects truncation.
func ReadFile(files fs.FS, name string, limit int64) ([]byte, error) {
	info, err := fs.Stat(files, name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("expected regular file: %s", name)
	}
	f, err := files.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	return data, err
}

// CommandOutput captures up to 64 KiB of combined command output. It can be
// shared by stdout and stderr writers.
type CommandOutput struct {
	mu        sync.Mutex
	data      []byte
	truncated bool
}

func (b *CommandOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := min(len(p), (64<<10)-len(b.data))
	b.data = append(b.data, p[:n]...)
	if n < len(p) {
		b.truncated = true
	}
	return len(p), nil
}
func (l *Local) Execute(ctx context.Context, c Command) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, c.Program, c.Args...)
	cmd.Dir = l.dir
	cmd.Env = l.env
	cmd.WaitDelay = time.Second
	var b CommandOutput
	cmd.Stdout = &b
	cmd.Stderr = &b
	err := cmd.Run()
	out := b.Result()
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		out.ExitCode = exit.ExitCode()
		return out, nil
	}
	return out, err
}

func (b *CommandOutput) Result() CommandResult {
	b.mu.Lock()
	defer b.mu.Unlock()
	return CommandResult{Output: string(b.data), Truncated: b.truncated}
}

// ReadWithin confines all symlink traversal for name to the selected directory.
func (l *Local) ReadWithin(dir, name string, limit int64) ([]byte, error) {
	if !fs.ValidPath(dir) || !fs.ValidPath(name) || name == "." || limit < 0 {
		return nil, fs.ErrInvalid
	}
	root, err := l.root.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return ReadFile(root.FS(), name, limit)
}
