package workspace

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Container shares a rooted local directory with disposable Docker or Podman
// containers. Files survive command boundaries. Each command gets a fresh
// process environment and container root filesystem.
type Container struct {
	*Local
	image, runtime string
}

// OpenContainer uses the host Docker-compatible CLI and daemon. The daemon must
// be on the same machine as dir. Image and runtime are application configuration.
func OpenContainer(dir, image, runtime string) (*Container, error) {
	if image == "" || strings.HasPrefix(image, "-") {
		return nil, errors.New("invalid container image")
	}
	if runtime == "" {
		runtime = "docker"
	}
	bin, err := exec.LookPath(runtime)
	if err != nil {
		return nil, err
	}
	local, err := Open(dir, nil)
	if err != nil {
		return nil, err
	}
	if strings.ContainsAny(local.dir, ",\n\r") {
		local.Close()
		return nil, errors.New("container mount path contains an unsupported character")
	}
	return &Container{Local: local, image: image, runtime: bin}, nil
}
func (w *Container) Execute(ctx context.Context, c Command) (CommandResult, error) {
	name := "go-llm-" + strings.ToLower(rand.Text())
	cmd := exec.CommandContext(ctx, w.runtime, append([]string{"run", "--rm", "--init", "--name", name, "--mount", "type=bind,src=" + w.dir + ",dst=/workspace", "--workdir", "/workspace", "--entrypoint", c.Program, w.image}, c.Args...)...)
	var b CommandOutput
	cmd.Stdout = &b
	cmd.Stderr = &b
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	out := b.Result()
	if ctx.Err() != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		// Killing the client can leave the container running on the daemon.
		_ = exec.CommandContext(cleanup, w.runtime, "rm", "--force", name).Run()
		return out, ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		out.ExitCode = exit.ExitCode()
		if out.ExitCode == 125 {
			return out, fmt.Errorf("container runtime: %s", out.Output)
		}
		return out, nil
	}
	return out, err
}
