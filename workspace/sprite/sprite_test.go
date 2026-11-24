package sprite_test

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/natexcvi/go-llm/workspace/sprite"
	sdk "github.com/superfly/sprites-go"
)

func TestSpriteReadsFromConfiguredDirectory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("workingDir") != "/work" || r.URL.Query().Get("path") != "note" {
			t.Error(r.URL)
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/fs/list"):
			_, _ = io.WriteString(w, `{"entries":[{"name":"note","mode":"0644","size":5,"isDir":false}]}`)
		case strings.HasSuffix(r.URL.Path, "/fs/read"):
			_, _ = io.WriteString(w, "hello")
		default:
			t.Error(r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := sdk.New("key", sdk.WithBaseURL(server.URL), sdk.WithHTTPClient(server.Client()), sdk.WithDisableControl())
	w, err := sprite.Open(client.Sprite("fixture"), "/work")
	if err != nil {
		t.Fatal(err)
	}
	b, err := fs.ReadFile(w, "note")
	if string(b) != "hello" || err != nil {
		t.Fatal(string(b), err)
	}
}
