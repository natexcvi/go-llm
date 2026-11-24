// Package browser drives Chromium through the Chrome DevTools Protocol.
package browser

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	h "github.com/natexcvi/go-llm"
)

// Chromium opens one tab per run and starts the browser on the first action.
// Origins must enumerate the HTTP(S) origins this tab may request. Configure
// deployment-level network isolation for the whole browser process.
// Browser resources close with the run and are recreated after a checkpoint.
type Chromium struct {
	// AllocatorOptions customize Chromium startup for the deployment.
	AllocatorOptions []chromedp.ExecAllocatorOption
	Origins          []string
	Executable       string
	Timeout          time.Duration
	// SavePNG persists screenshots and returns a reference for the model.
	SavePNG func(context.Context, []byte) (string, error)
}

func origin(address string) (string, error) {
	u, err := url.Parse(address)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("an HTTP(S) URL without user information is required")
	}
	host := strings.ToLower(u.Host)
	if u.Scheme == "http" {
		host = strings.TrimSuffix(host, ":80")
	} else {
		host = strings.TrimSuffix(host, ":443")
	}
	return u.Scheme + "://" + host, nil
}

type tab struct {
	lifetime context.Context
	cfg      Chromium
	allowed  map[string]bool
	ctx      context.Context
	close    func()
}

func (t *tab) permits(address string) bool {
	key, err := origin(address)
	return err == nil && t.allowed[key]
}
func (t *tab) start() error {
	if t.ctx != nil {
		return nil
	}
	opts := slices.Clone(chromedp.DefaultExecAllocatorOptions[:])
	if t.cfg.Executable != "" {
		opts = append(opts, chromedp.ExecPath(t.cfg.Executable))
	}
	opts = append(opts, t.cfg.AllocatorOptions...)
	allocator, stopAllocator := chromedp.NewExecAllocator(t.lifetime, opts...)
	page, stopPage := chromedp.NewContext(allocator)
	closePage := func() {
		stopPage()
		stopAllocator()
	}
	chromedp.ListenTarget(page, func(ev any) {
		request, ok := ev.(*fetch.EventRequestPaused)
		if !ok {
			return
		}
		go func() {
			target := chromedp.FromContext(page).Target
			ctx := cdp.WithExecutor(page, target)
			if t.permits(request.Request.URL) {
				_ = fetch.ContinueRequest(request.RequestID).Do(ctx)
			} else {
				_ = fetch.FailRequest(request.RequestID, network.ErrorReasonBlockedByClient).Do(ctx)
			}
		}()
	})
	if err := chromedp.Run(page, network.Enable(), network.SetBypassServiceWorker(true), fetch.Enable()); err != nil {
		closePage()
		return err
	}
	t.ctx = page
	t.close = closePage
	return nil
}
func (t *tab) run(ctx context.Context, actions ...chromedp.Action) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := t.start(); err != nil {
		return err
	}
	window, cancel := context.WithTimeout(t.ctx, t.cfg.Timeout)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	if err := chromedp.Run(window, actions...); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return h.Retry(err)
	}
	return nil
}

type pageState struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

const visiblePage = `({url:location.href,title:document.title,text:(document.body?.innerText||"").slice(0,16000)})`

func (t *tab) state(ctx context.Context, actions ...chromedp.Action) (pageState, error) {
	var s pageState
	err := t.run(ctx, append(actions, chromedp.Evaluate(visiblePage, &s))...)
	return s, err
}

type selector struct {
	Selector string `json:"selector"`
}
type empty struct{}

// Capability registers navigation, interaction, reading and optional screenshot tools.
func (c Chromium) Capability() h.Capability {
	c.Origins = slices.Clone(c.Origins)
	c.AllocatorOptions = slices.Clone(c.AllocatorOptions)
	return func(ctx context.Context, r *h.Run) error {
		if len(c.Origins) == 0 || c.Timeout < 0 {
			return errors.New("Chromium requires allowed origins and a nonnegative timeout")
		}
		cfg := c
		if cfg.Timeout == 0 {
			cfg.Timeout = 15 * time.Second
		}
		t := tab{lifetime: ctx, cfg: cfg, allowed: map[string]bool{}}
		for _, address := range cfg.Origins {
			key, err := origin(address)
			if err != nil {
				return err
			}
			u, _ := url.Parse(address)
			if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
				return fmt.Errorf("expected an origin without a path: %s", address)
			}
			t.allowed[key] = true
		}
		r.OnClose = append(r.OnClose, func() error {
			if t.close != nil {
				t.close()
			}
			return nil
		})
		tools := []h.Tool{
			h.NewTool("navigate", "Navigate to an allowed HTTP(S) URL and return visible text.", func(ctx context.Context, in struct {
				URL string `json:"url"`
			}) (pageState, error) {
				if !t.permits(in.URL) {
					return pageState{}, h.Retry(errors.New("URL outside configured origins"))
				}
				return t.state(ctx, chromedp.Navigate(in.URL))
			}),
			h.NewTool("get_text", "Read visible page or element text using a CSS selector.", func(ctx context.Context, in struct {
				Selector string `json:"selector,omitempty"`
			}) (string, error) {
				if in.Selector == "" {
					s, err := t.state(ctx)
					return s.Text, err
				}
				var text string
				err := t.run(ctx, chromedp.Text(in.Selector, &text, chromedp.ByQuery, chromedp.NodeVisible))
				runes := []rune(text)
				return string(runes[:min(len(runes), 16000)]), err
			}),
			h.NewTool("click", "Click a visible CSS selector.", func(ctx context.Context, in selector) (pageState, error) {
				return t.state(ctx, chromedp.Click(in.Selector, chromedp.ByQuery))
			}),
			h.NewTool("type_text", "Replace a field with text and dispatch input events.", func(ctx context.Context, in struct {
				Selector string `json:"selector"`
				Text     string `json:"text"`
			}) (pageState, error) {
				return t.state(ctx, chromedp.Clear(in.Selector, chromedp.ByQuery), chromedp.SendKeys(in.Selector, in.Text, chromedp.ByQuery))
			}),
			h.NewTool("wait_for", "Wait for a CSS selector to become visible or disappear.", func(ctx context.Context, in struct {
				Selector string `json:"selector"`
				Gone     bool   `json:"gone,omitempty"`
			}) (pageState, error) {
				if in.Gone {
					return t.state(ctx, chromedp.WaitNotVisible(in.Selector, chromedp.ByQuery))
				}
				return t.state(ctx, chromedp.WaitVisible(in.Selector, chromedp.ByQuery))
			}),
			h.NewTool("go_back", "Navigate backward in the active tab.", func(ctx context.Context, _ empty) (pageState, error) { return t.state(ctx, chromedp.NavigateBack()) }),
			h.NewTool("go_forward", "Navigate forward in the active tab.", func(ctx context.Context, _ empty) (pageState, error) { return t.state(ctx, chromedp.NavigateForward()) }),
			h.NewTool("scroll", "Scroll a matching element into view.", func(ctx context.Context, in selector) (pageState, error) {
				return t.state(ctx, chromedp.ScrollIntoView(in.Selector, chromedp.ByQuery))
			}),
		}
		if cfg.SavePNG != nil {
			tools = append(tools, h.NewTool("screenshot", "Save a PNG screenshot and return its reference.", func(ctx context.Context, in struct {
				FullPage bool `json:"full_page,omitempty"`
			}) (string, error) {
				var data []byte
				action := chromedp.CaptureScreenshot(&data)
				if in.FullPage {
					action = chromedp.FullScreenshot(&data, 100)
				}
				if err := t.run(ctx, action); err != nil {
					return "", err
				}
				return cfg.SavePNG(ctx, data)
			}))
		}
		return r.Register(tools...)
	}
}
