package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"time"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/workspace"
)

// PydanticAIDocs exposes the six upstream documentation topics. Local, when
// set, is rooted at the docs directory. Missing pages fall back to GitHub.
// Ref defaults to the Pydantic AI version paired with Harness 0.73.0.
type PydanticAIDocs struct {
	Local        fs.FS
	Client       *http.Client
	Ref          string
	DisableCache bool
}

var docPages = map[string]string{"capabilities": "capabilities/overview.md", "hooks": "hooks.md", "tools": "tools.md", "tools-advanced": "tools-advanced.md", "toolsets": "toolsets.md", "agent": "agent.md"}
var modelAlias = regexp.MustCompile(`\{\{ ([a-z0-9-]+:[a-z]+)(\.name)? \}\}`)

func (d PydanticAIDocs) read(ctx context.Context, name string) (string, error) {
	if d.Local != nil {
		data, err := workspace.ReadFile(d.Local, name, 4<<20)
		if err == nil {
			return string(data), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	}
	ref := d.Ref
	if ref == "" {
		ref = "v2.73.0"
	}
	if !fs.ValidPath(ref) || strings.ContainsAny(ref, "?#") {
		return "", errors.New("invalid documentation ref")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", "https://raw.githubusercontent.com/pydantic/pydantic-ai/"+ref+"/docs/"+name, nil)
	if err != nil {
		return "", err
	}
	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("documentation fetch %s: %s", name, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20+1))
	if len(b) > 4<<20 {
		return "", errors.New("documentation page exceeds 4 MiB")
	}
	return string(b), err
}
func (d PydanticAIDocs) Capability() h.Capability {
	return func(ctx context.Context, r *h.Run) error {
		cache := map[string]string{}
		r.Instructions = append(r.Instructions, "Use read_pyai_docs for Pydantic AI API and capability documentation.")
		return r.Register(h.NewTool("read_pyai_docs", "Read a Pydantic AI documentation topic.", func(ctx context.Context, in struct {
			Topic string `json:"topic" jsonschema:"enum=capabilities,enum=hooks,enum=tools,enum=tools-advanced,enum=toolsets,enum=agent"`
		}) (string, error) {
			page, ok := docPages[in.Topic]
			if !ok {
				return "", h.Retry(errors.New("unknown documentation topic"))
			}
			if s, ok := cache[page]; ok {
				return s, nil
			}
			body, err := d.read(ctx, page)
			if err != nil {
				return "", err
			}
			if modelAlias.MatchString(body) {
				data, err := d.read(ctx, "model_aliases/aliases.json")
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				if err == nil {
					var aliases map[string]string
					if err := json.Unmarshal([]byte(data), &aliases); err != nil {
						return "", err
					}
					body = modelAlias.ReplaceAllStringFunc(body, func(m string) string {
						parts := modelAlias.FindStringSubmatch(m)
						model := aliases[parts[1]]
						if model == "" {
							return m
						}
						if parts[2] != "" {
							_, model, _ = strings.Cut(model, ":")
						}
						return model
					})
				}
			}
			if !d.DisableCache {
				cache[page] = body
			}
			return body, nil
		}))
	}
}
