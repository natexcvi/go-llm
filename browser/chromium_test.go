package browser_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/browser"
)

func TestChromiumInteractionAndSubresourcePolicy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bin := os.Getenv("GO_LLM_TEST_CHROME")
	if bin == "" {
		for _, name := range []string{"google-chrome", "chromium", "chromium-browser"} {
			if p, err := exec.LookPath(name); err == nil {
				bin = p
				break
			}
		}
		if bin == "" {
			t.Skip("Chromium is required for this integration test")
		}
	}
	var blockedHits atomic.Int64
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { blockedHits.Add(1) }))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprintf(w, `<input id="name"><button onclick="document.querySelector('p').textContent=document.querySelector('input').value">Save</button><p>initial</p><img src="%s/blocked">`, other.URL)
	}))
	defer server.Close()
	steps := []h.Call{{Name: "navigate", ID: "1", Arguments: fmt.Sprintf(`{"url":%q}`, server.URL)}, {Name: "type_text", ID: "2", Arguments: `{"selector":"#name","text":"saved"}`}, {Name: "click", ID: "3", Arguments: `{"selector":"button"}`}, {Name: "get_text", ID: "4", Arguments: `{"selector":"p"}`}}
	model := h.ModelFunc(func(_ context.Context, q h.Request) (h.Response, error) {
		if len(steps) > 0 {
			c := steps[0]
			steps = steps[1:]
			return h.Response{Calls: []h.Call{c}}, nil
		}
		return h.Response{Text: q.Messages[len(q.Messages)-1].Text}, nil
	})
	agent := h.Agent[string]{Output: h.Text(), Model: model, Capabilities: []h.Capability{browser.Chromium{Origins: []string{server.URL}, Executable: bin, AllocatorOptions: []chromedp.ExecAllocatorOption{chromedp.NoSandbox, chromedp.WSURLReadTimeout(time.Minute)}}.Capability()}}
	result, err := agent.Run(t.Context(), "fill the form", h.Options{})
	if err != nil || result.Output != "saved" || blockedHits.Load() != 0 {
		t.Fatal(result.Output, err, blockedHits.Load())
	}
}

func TestDelegatedBrowserUsesChildModelAndPolicy(t *testing.T) {
	child := h.ModelFunc(func(_ context.Context, q h.Request) (h.Response, error) {
		if !slices.ContainsFunc(q.Tools, func(d h.Definition) bool { return d.Name == "navigate" }) {
			t.Fatal("missing navigation tool")
		}
		last := q.Messages[len(q.Messages)-1]
		if last.Role == h.ToolResult {
			return h.Response{Text: last.Text}, nil
		}
		return h.Response{Calls: []h.Call{{ID: "nav", Name: "navigate", Arguments: `{"url":"https://example.com/"}`}}}, nil
	})
	b := browser.BrowserUse{Browser: browser.Chromium{Origins: []string{"https://example.com"}}, Model: child}
	a := h.Agent[string]{Output: h.Text(), Capabilities: []h.Capability{b.Capability()}, Model: h.ModelFunc(func(_ context.Context, q h.Request) (h.Response, error) {
		if len(q.Tools) != 1 || q.Tools[0].Name != "browse_web" {
			t.Fatal(q.Tools)
		}
		last := q.Messages[len(q.Messages)-1]
		if last.Role == h.ToolResult {
			return h.Response{Text: last.Text}, nil
		}
		return h.Response{Calls: []h.Call{{Name: "browse_web", ID: "task", Arguments: `{"task":"read the home page"}`}}}, nil
	})}
	out, err := a.Run(t.Context(), "browse", h.Options{Authorize: func(_ context.Context, c h.Call) (h.Decision, error) {
		if c.Name == "navigate" {
			return h.Deny, nil
		}
		return h.Approve, nil
	}})
	if err != nil || !strings.Contains(out.Output, "denied") {
		t.Fatal(out, err)
	}
}
