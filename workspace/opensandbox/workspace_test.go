package opensandbox_test

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/natexcvi/go-llm/workspace"
	"github.com/natexcvi/go-llm/workspace/opensandbox"
)

func TestReadAndExecute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/endpoints/"):
			_, _ = fmt.Fprintf(w, `{"endpoint":%q}`, r.Host)
		case r.URL.Path == "/files/info":
			if r.URL.Query().Get("path") != "/work/note" {
				t.Error(r.URL)
			}
			_, _ = io.WriteString(w, `{"/work/note":{"path":"/work/note","size":5,"mode":644,"type":"file"}}`)
		case r.URL.Path == "/files/download":
			_, _ = io.WriteString(w, "hello")
		case r.URL.Path == "/command":
			var input sdk.RunCommandRequest
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Cwd != "/work" || len(input.Argv) != 2 || input.Argv[1] != "x y" {
				t.Error(input, err)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"type\":\"stdout\",\"text\":\"ok\"}\n\ndata: {\"type\":\"execution_complete\",\"exit_code\":0}\n\n")
		default:
			t.Error(r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	box, err := sdk.ConnectSandbox(t.Context(), sdk.ConnectionConfig{Domain: strings.TrimPrefix(server.URL, "http://"), Protocol: "http", APIKey: "test"}, "box")
	if err != nil {
		t.Fatal(err)
	}
	w, err := opensandbox.Open(t.Context(), box, "/work")
	if err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile(w, "note")
	if err != nil || string(data) != "hello" {
		t.Fatal(string(data), err)
	}
	res, err := w.Execute(t.Context(), workspace.Command{Program: "echo", Args: []string{"x y"}})
	if err != nil || res.ExitCode != 0 || res.Output != "ok" {
		t.Fatal(res, err)
	}
}
