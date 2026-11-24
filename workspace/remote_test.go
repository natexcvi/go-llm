package workspace_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/natexcvi/go-llm/workspace"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func TestSSHFilesAndCommand(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(key)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	commands := make(chan string, 1)
	go func() {
		sock, err := l.Accept()
		if err != nil {
			return
		}
		conn, channels, reqs, err := ssh.NewServerConn(sock, cfg)
		if err != nil {
			sock.Close()
			return
		}
		defer conn.Close()
		go ssh.DiscardRequests(reqs)
		for newChannel := range channels {
			ch, requests, err := newChannel.Accept()
			if err != nil {
				return
			}
			go func() {
				defer ch.Close()
				for req := range requests {
					switch req.Type {
					case "subsystem":
						req.Reply(true, nil)
						server, err := sftp.NewServer(ch)
						if err != nil {
							return
						}
						server.Serve()
						server.Close()
						return
					case "exec":
						var cmd struct{ Command string }
						ssh.Unmarshal(req.Payload, &cmd)
						commands <- cmd.Command
						req.Reply(true, nil)
						fmt.Fprint(ch, "ok")
						ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
						return
					default:
						req.Reply(false, nil)
					}
				}
			}()
		}
	}()
	client, err := ssh.Dial("tcp", l.Addr().String(), &ssh.ClientConfig{User: "test", HostKeyCallback: ssh.FixedHostKey(key.PublicKey())})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	w, err := workspace.OpenSSH(client, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err = w.WriteFile("docs/a", []byte("before")); err != nil {
		t.Fatal(err)
	}
	if err = w.Edit("docs/a", "before", "after"); err != nil {
		t.Fatal(err)
	}
	b, err := workspace.ReadFile(w, "docs/a", 64)
	if err != nil || string(b) != "after" {
		t.Fatal(string(b), err)
	}
	out, err := w.Execute(t.Context(), workspace.Command{Program: "printf", Args: []string{"a'b"}})
	if err != nil || out.Output != "ok" {
		t.Fatal(out, err)
	}
	if cmd := <-commands; !strings.HasSuffix(cmd, `&& exec 'printf' 'a'"'"'b'`) {
		t.Error(cmd)
	}
}
func TestDockerWorkspace(t *testing.T) {
	image := os.Getenv("GO_LLM_DOCKER_TEST_IMAGE")
	if image == "" {
		t.Skip("set GO_LLM_DOCKER_TEST_IMAGE to an image containing cat")
	}
	w, err := workspace.OpenContainer(t.TempDir(), image, "docker")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err = w.WriteFile("hello", []byte("from workspace")); err != nil {
		t.Fatal(err)
	}
	out, err := w.Execute(context.Background(), workspace.Command{Program: "cat", Args: []string{"hello"}})
	if err != nil || out.ExitCode != 0 || out.Output != "from workspace" {
		t.Fatal(out, err)
	}
}
