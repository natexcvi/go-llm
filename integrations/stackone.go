package integrations

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	h "github.com/natexcvi/go-llm"
)

// StackOne exposes MCP actions for a single linked account. Credentials is
// evaluated separately for each agent run. Actions contains case-insensitive
// shell globs. An empty list defaults to the server's search_execute mode.
type StackOne struct {
	BaseURL, Prefix, Mode string
	Client                *http.Client
	Actions               []string
	Credentials           func(context.Context) (accountID, apiKey string, err error)
}
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func (s StackOne) Capability() h.Capability {
	patterns := slices.Clone(s.Actions)
	return func(ctx context.Context, r *h.Run) error {
		base := s.BaseURL
		if base == "" {
			base = "https://api.stackone.com"
		}
		u, err := url.Parse(base)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("invalid StackOne HTTPS base URL")
		}
		u = u.JoinPath("mcp")
		mode := s.Mode
		if mode == "" {
			mode = "individual"
			if len(patterns) == 0 {
				mode = "search_execute"
			}
		}
		switch mode {
		case "search_execute":
			if len(patterns) > 0 {
				return errors.New("action filters require individual mode")
			}
			u.RawQuery = "tool-mode=search_execute"
		case "individual":
		default:
			return errors.New("invalid StackOne tool mode")
		}
		for _, p := range patterns {
			if _, err := path.Match(p, ""); err != nil {
				return err
			}
		}
		if s.Credentials == nil {
			return errors.New("StackOne credentials required")
		}
		account, key, err := s.Credentials(ctx)
		if err != nil {
			return err
		}
		if account == "" || key == "" {
			return errors.New("StackOne requires an account and API key")
		}
		client := http.DefaultClient
		if s.Client != nil {
			client = s.Client
		}
		configured := *client
		transport := configured.Transport
		if transport == nil {
			transport = http.DefaultTransport
		}
		configured.Transport = roundTripFunc(func(q *http.Request) (*http.Response, error) {
			if q.URL.Scheme != u.Scheme || !strings.EqualFold(q.URL.Host, u.Host) {
				return nil, errors.New("StackOne redirected outside its configured origin")
			}
			q = q.Clone(q.Context())
			q.SetBasicAuth(key, "")
			q.Header.Set("x-account-id", account)
			return transport.RoundTrip(q)
		})
		conn := MCP{Prefix: s.Prefix, IncludeInstructions: true, Transport: func(context.Context) (mcp.Transport, error) {
			return &mcp.StreamableClientTransport{Endpoint: u.String(), HTTPClient: &configured}, nil
		}}
		if len(patterns) > 0 {
			conn.Allow = func(t *mcp.Tool) bool {
				for _, p := range patterns {
					if ok, _ := path.Match(strings.ToLower(p), strings.ToLower(t.Name)); ok {
						return true
					}
				}
				return false
			}
		}
		return conn.Capability()(ctx, r)
	}
}
