package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
)

// Bubblewrap confines commands in Linux namespaces, mounts system binaries
// read-only, creates private /tmp, /dev and /proc, and binds the local workspace
// at /workspace. It requires an installed bwrap permitted to create namespaces.
type Bubblewrap struct {
	*Local
	bin     string
	network bool
}

func OpenBubblewrap(dir string, allowNetwork bool) (*Bubblewrap, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("bubblewrap requires Linux")
	}
	bin, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, err
	}
	w, err := Open(dir, nil)
	if err != nil {
		return nil, err
	}
	return &Bubblewrap{w, bin, allowNetwork}, nil
}
func (w *Bubblewrap) Execute(ctx context.Context, c Command) (CommandResult, error) {
	args := []string{"--die-with-parent", "--new-session", "--unshare-all"}
	if w.network {
		args = append(args, "--share-net")
	}
	args = append(args, "--ro-bind", "/usr", "/usr")
	for _, p := range []string{"/bin", "/sbin", "/lib", "/lib64", "/etc/ssl/certs"} {
		if _, err := os.Stat(p); err == nil {
			args = append(args, "--ro-bind", p, p)
		}
	}
	if w.network {
		args = append(args, "--ro-bind", "/etc/resolv.conf", "/etc/resolv.conf")
	}
	args = append(args, "--dev", "/dev", "--proc", "/proc", "--tmpfs", "/tmp", "--bind", w.dir, "/workspace", "--chdir", "/workspace", "--setenv", "PATH", "/usr/local/bin:/usr/bin:/bin", "--setenv", "HOME", "/tmp", "--", c.Program)
	return w.Local.Execute(ctx, Command{Program: w.bin, Args: append(args, c.Args...)})
}
