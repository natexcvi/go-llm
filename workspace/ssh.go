package workspace

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// SSH runs commands and file operations over an already authenticated SSH
// connection to a POSIX host. The caller owns and verifies the SSH connection.
// Use server-side isolation for untrusted users sharing a remote filesystem.
type SSH struct {
	client *ssh.Client
	files  *sftp.Client
	root   string
	mu     sync.Mutex
}

func OpenSSH(client *ssh.Client, dir string) (*SSH, error) {
	if client == nil {
		return nil, errors.New("SSH connection required")
	}
	files, err := sftp.NewClient(client)
	if err != nil {
		return nil, err
	}
	if dir == "" {
		dir = "."
	}
	root, err := files.RealPath(dir)
	if err == nil {
		var info fs.FileInfo
		info, err = files.Stat(root)
		if err == nil && !info.IsDir() {
			err = fmt.Errorf("%q is not a directory", dir)
		}
	}
	if err != nil {
		files.Close()
		return nil, err
	}
	return &SSH{client: client, files: files, root: root}, nil
}
func (w *SSH) Close() error { return w.files.Close() }

// SFTP does not provide openat-style scoped opens. Reject symlink components;
// an adversarial concurrent remote writer requires a server-side chroot.
func (w *SSH) resolve(name string) (string, error) {
	if !fs.ValidPath(name) {
		return "", fs.ErrInvalid
	}
	p := w.root
	for _, part := range strings.Split(name, "/") {
		p = path.Join(p, part)
		stat, err := w.files.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if stat.Mode()&fs.ModeSymlink != 0 {
			return "", fmt.Errorf("symbolic link in workspace path %q", name)
		}
	}
	return p, nil
}
func (w *SSH) Open(name string) (fs.File, error) {
	p, err := w.resolve(name)
	if err != nil {
		return nil, err
	}
	return w.files.Open(p)
}
func (w *SSH) Stat(name string) (fs.FileInfo, error) {
	p, err := w.resolve(name)
	if err != nil {
		return nil, err
	}
	return w.files.Stat(p)
}
func (w *SSH) ReadDir(name string) ([]fs.DirEntry, error) {
	p, err := w.resolve(name)
	if err != nil {
		return nil, err
	}
	infos, err := w.files.ReadDir(p)
	if err != nil {
		return nil, err
	}
	entries := make([]fs.DirEntry, len(infos))
	for i, info := range infos {
		entries[i] = fs.FileInfoToDirEntry(info)
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries, nil
}
func (w *SSH) WriteFile(name string, data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.write(name, data)
}
func (w *SSH) write(name string, data []byte) error {
	p, err := w.resolve(name)
	if err != nil {
		return err
	}
	mode := fs.FileMode(0600)
	exists := false
	if info, err := w.files.Lstat(p); err == nil {
		if !info.Mode().IsRegular() {
			return fs.ErrInvalid
		}
		mode = info.Mode().Perm()
		exists = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := w.files.MkdirAll(path.Dir(p)); err != nil {
		return err
	}
	tmp := path.Join(path.Dir(p), ".go-llm-"+rand.Text())
	f, err := w.files.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if err != nil {
		return err
	}
	defer w.files.Remove(tmp)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	if !exists {
		return w.files.Rename(tmp, p)
	}
	return w.files.PosixRename(tmp, p)
}
func (w *SSH) Edit(name, old, replacement string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	data, err := ReadFile(w, name, 8<<20)
	if err != nil {
		return err
	}
	if old == "" || strings.Count(string(data), old) != 1 {
		return errors.New("old text must occur exactly once")
	}
	return w.write(name, []byte(strings.Replace(string(data), old, replacement, 1)))
}
func quotePOSIX(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func (w *SSH) Execute(ctx context.Context, c Command) (CommandResult, error) {
	if err := ctx.Err(); err != nil {
		return CommandResult{}, err
	}
	session, err := w.client.NewSession()
	if err != nil {
		return CommandResult{}, err
	}
	defer session.Close()
	argv := append([]string{c.Program}, c.Args...)
	for i, s := range argv {
		if strings.ContainsRune(s, '\x00') {
			return CommandResult{}, fs.ErrInvalid
		}
		argv[i] = quotePOSIX(s)
	}
	var b CommandOutput
	session.Stdout = &b
	session.Stderr = &b
	if err = session.Start("cd " + quotePOSIX(w.root) + " && exec " + strings.Join(argv, " ")); err != nil {
		return CommandResult{}, err
	}
	done := make(chan error, 1)
	go func() { done <- session.Wait() }()
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		<-done
		return CommandResult{}, ctx.Err()
	}
	out := b.Result()
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		out.ExitCode = exit.ExitStatus()
		return out, nil
	}
	return out, err
}
