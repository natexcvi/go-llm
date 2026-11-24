// Package opensandbox adapts an existing OpenSandbox instance to the harness.
// Create or reconnect the instance using the OpenSandbox Go SDK. The caller
// owns that instance and its lifecycle context.
package opensandbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/natexcvi/go-llm/workspace"
)

type Workspace struct {
	box      *sdk.Sandbox
	dir      string
	lifetime context.Context
	mu       sync.Mutex
}

func Open(lifetime context.Context, box *sdk.Sandbox, dir string) (*Workspace, error) {
	if lifetime == nil || box == nil || !path.IsAbs(dir) {
		return nil, errors.New("lifetime, sandbox and absolute working directory required")
	}
	return &Workspace{box: box, dir: path.Clean(dir), lifetime: lifetime}, nil
}

type info struct{ sdk.FileInfo }

func (i info) Name() string       { return path.Base(i.Path) }
func (i info) Size() int64        { return i.FileInfo.Size }
func (i info) ModTime() time.Time { return i.ModifiedAt }
func (i info) Mode() fs.FileMode {
	n, _ := strconv.ParseUint(strconv.Itoa(i.FileInfo.Mode), 8, 32)
	m := fs.FileMode(n)
	switch i.Type {
	case "directory", "dir":
		m |= fs.ModeDir
	case "symlink":
		m |= fs.ModeSymlink
	}
	return m
}
func (i info) IsDir() bool { return i.Mode().IsDir() }
func (i info) Sys() any    { return nil }

type entry struct {
	fs.FileInfo
	io.ReadCloser
}

func (e entry) Stat() (fs.FileInfo, error) { return e.FileInfo, nil }
func (w *Workspace) Stat(name string) (fs.FileInfo, error) {
	if !fs.ValidPath(name) {
		return nil, fs.ErrInvalid
	}
	p := path.Join(w.dir, name)
	v, err := w.box.GetFileInfo(w.lifetime, p)
	if err != nil {
		return nil, fileErr(err)
	}
	i, ok := v[p]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return info{i}, nil
}
func fileErr(err error) error {
	var e *sdk.APIError
	if errors.As(err, &e) && e.StatusCode == 404 {
		return fs.ErrNotExist
	}
	return err
}
func (w *Workspace) Open(name string) (fs.File, error) {
	i, err := w.Stat(name)
	if err != nil {
		return nil, err
	}
	if i.IsDir() {
		return entry{i, io.NopCloser(strings.NewReader(""))}, nil
	}
	reader, err := w.box.DownloadFile(w.lifetime, path.Join(w.dir, name), "")
	if err != nil {
		return nil, fileErr(err)
	}
	return entry{i, reader}, nil
}
func (w *Workspace) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) {
		return nil, fs.ErrInvalid
	}
	dir := path.Join(w.dir, name)
	items, err := w.box.ListDirectoryWithDepth(w.lifetime, dir, 1)
	if err != nil {
		return nil, fileErr(err)
	}
	entries := make([]fs.DirEntry, 0, len(items))
	for _, i := range items {
		if path.Dir(i.Path) == dir {
			entries = append(entries, fs.FileInfoToDirEntry(info{i}))
		}
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries, nil
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
	p := path.Join(w.dir, name)
	mode := fs.FileMode(0600)
	i, err := w.Stat(name)
	if err == nil {
		if !i.Mode().IsRegular() {
			return fs.ErrInvalid
		}
		mode = i.Mode().Perm()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if path.Dir(p) != w.dir {
		if _, err := w.Stat(path.Dir(name)); errors.Is(err, fs.ErrNotExist) {
			if err = w.box.CreateDirectory(w.lifetime, path.Dir(p), 700); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	tmp := path.Join(path.Dir(p), ".go-llm-"+rand.Text())
	opts := sdk.UploadFileOptions{FileName: path.Base(tmp), Metadata: sdk.FileMetadata{Path: tmp, Mode: sdk.OctalMode(mode)}}
	if err := w.box.UploadFile(w.lifetime, bytes.NewReader(data), opts); err != nil {
		return err
	}
	if err := w.box.MoveFiles(w.lifetime, sdk.MoveRequest{{Src: tmp, Dest: p}}); err != nil {
		_ = w.box.DeleteFiles(w.lifetime, []string{tmp})
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
	req := sdk.RunCommandRequest{Argv: append([]string{c.Program}, c.Args...), Cwd: w.dir}
	if deadline, ok := ctx.Deadline(); ok {
		req.Timeout = max(1, time.Until(deadline).Milliseconds())
	}
	var b workspace.CommandOutput
	write := func(m sdk.OutputMessage) error {
		_, err := io.WriteString(&b, m.Text)
		return err
	}
	res, err := w.box.RunCommandWithOpts(ctx, req, &sdk.ExecutionHandlers{OnStdout: write, OnStderr: write, SkipAccumulation: true})
	out := b.Result()
	if err != nil {
		return out, err
	}
	if res.ExitCode != nil {
		out.ExitCode = *res.ExitCode
		return out, nil
	}
	if res.Error != nil {
		return out, fmt.Errorf("opensandbox command: %s: %s", res.Error.Name, res.Error.Value)
	}
	if res.Complete == nil {
		return out, io.ErrUnexpectedEOF
	}
	return out, nil
}
