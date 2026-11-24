// Package sprite adapts a Fly.io Sprite to the harness workspace interfaces.
// The caller provisions or attaches the Sprite with the official Go SDK and
// controls its lifetime. Files and processes live on that Sprite.
package sprite

import (
	"context"
	"crypto/rand"
	"errors"
	"io/fs"
	"path"
	"strings"
	"sync"

	"github.com/natexcvi/go-llm/workspace"
	sdk "github.com/superfly/sprites-go"
)

type Workspace struct {
	sprite *sdk.Sprite
	dir    string
	files  sdk.FS
	mu     sync.Mutex
}

func Open(sprite *sdk.Sprite, dir string) (*Workspace, error) {
	if sprite == nil || !path.IsAbs(dir) {
		return nil, errors.New("sprite and absolute working directory required")
	}
	return &Workspace{sprite: sprite, dir: dir, files: sprite.FilesystemAt(dir)}, nil
}
func (w *Workspace) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, fs.ErrInvalid
	}
	return w.files.Open(name)
}
func (w *Workspace) Stat(name string) (fs.FileInfo, error) {
	if !fs.ValidPath(name) {
		return nil, fs.ErrInvalid
	}
	return w.files.Stat(name)
}
func (w *Workspace) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) {
		return nil, fs.ErrInvalid
	}
	return w.files.ReadDir(name)
}
func (w *Workspace) WriteFile(name string, data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.write(name, data)
}
func (w *Workspace) write(name string, data []byte) error {
	if !fs.ValidPath(name) || name == "." {
		return fs.ErrInvalid
	}
	mode := fs.FileMode(0600)
	info, err := w.Stat(name)
	if err == nil {
		if !info.Mode().IsRegular() {
			return fs.ErrInvalid
		}
		mode = info.Mode().Perm()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	tmp := path.Join(path.Dir(name), ".go-llm-"+rand.Text())
	if err := w.files.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := w.files.Rename(tmp, name); err != nil {
		_ = w.files.Remove(tmp)
		return err
	}
	return nil
}
func (w *Workspace) Edit(name, old, replacement string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	b, err := workspace.ReadFile(w, name, 8<<20)
	if err != nil {
		return err
	}
	if old == "" || strings.Count(string(b), old) != 1 {
		return errors.New("old text must occur exactly once")
	}
	return w.write(name, []byte(strings.Replace(string(b), old, replacement, 1)))
}
func (w *Workspace) Execute(ctx context.Context, c workspace.Command) (workspace.CommandResult, error) {
	cmd := w.sprite.CommandContext(ctx, c.Program, c.Args...)
	cmd.Dir = w.dir
	var output workspace.CommandOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	out := output.Result()
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	var exit *sdk.ExitError
	if errors.As(err, &exit) {
		out.ExitCode = exit.ExitCode()
		return out, nil
	}
	return out, err
}
