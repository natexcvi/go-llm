// Package integrations connects native Go agents to hosted and local MCP servers.
package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	h "github.com/natexcvi/go-llm"
)

// MCP opens a fresh session for each run, discovers all pages of tools, and
// closes the session even if capability initialization or a call fails.
type MCP struct {
	Transport           func(context.Context) (mcp.Transport, error)
	Prefix              string
	ReadOnly            bool
	IncludeInstructions bool
	Allow               func(*mcp.Tool) bool
}

func (m MCP) Capability() h.Capability {
	return func(ctx context.Context, r *h.Run) error {
		if m.Transport == nil {
			return errors.New("missing MCP transport")
		}
		t, err := m.Transport(ctx)
		if err != nil {
			return err
		}
		client := mcp.NewClient(&mcp.Implementation{Name: "go-llm", Version: "1"}, nil)
		session, err := client.Connect(ctx, t, nil)
		if err != nil {
			return err
		}
		r.OnClose = append(r.OnClose, session.Close)
		if m.IncludeInstructions && session.InitializeResult().Instructions != "" {
			r.Instructions = append(r.Instructions, session.InitializeResult().Instructions)
		}
		for remote, err := range session.Tools(ctx, nil) {
			if err != nil {
				return err
			}
			if m.ReadOnly && (remote.Annotations == nil || !remote.Annotations.ReadOnlyHint) {
				continue
			}
			if m.Allow != nil && !m.Allow(remote) {
				continue
			}
			schema, err := json.Marshal(remote.InputSchema)
			if err != nil {
				return err
			}
			name := remote.Name
			if m.Prefix != "" {
				name = m.Prefix + "_" + name
			}
			tool := h.Tool{Definition: h.Definition{Name: name, Description: remote.Description, Schema: schema}, Execute: func(ctx context.Context, c h.Call) (string, error) {
				args := json.RawMessage(c.Arguments)
				if !json.Valid(args) {
					return "", h.Retry(errors.New("invalid JSON tool arguments"))
				}
				out, err := session.CallTool(ctx, &mcp.CallToolParams{Name: remote.Name, Arguments: args})
				if err != nil {
					return "", err
				}
				if out.IsError {
					return "", h.Retry(fmt.Errorf("%s: %s", name, renderContent(out)))
				}
				if out.StructuredContent != nil {
					b, err := json.Marshal(out.StructuredContent)
					return string(b), err
				}
				return renderContent(out), nil
			}}
			if err := r.Register(tool); err != nil {
				return err
			}
		}
		return nil
	}
}
func renderContent(out *mcp.CallToolResult) string {
	var parts []string
	for _, c := range out.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, t.Text)
		} else {
			raw, _ := json.Marshal(c)
			parts = append(parts, string(raw))
		}
	}
	return strings.Join(parts, "\n")
}

// Hosted services use the upstream Streamable HTTP endpoints. Supply an
// authenticated http.Client (for example oauth2.NewClient). Its credentials
// and transport are owned by the application.
var endpoints = map[string]string{
	"github": "https://api.githubcopilot.com/mcp/", "linear": "https://mcp.linear.app/mcp", "notion": "https://mcp.notion.com/mcp", "slack": "https://mcp.slack.com/mcp",
	"grain": "https://api.grain.com/_/mcp", "day_ai": "https://day.ai/api/mcp", "pylon": "https://mcp.usepylon.com", "posthog": "https://mcp.posthog.com/mcp", "ordinal": "https://app.tryordinal.com/mcp",
	"logfire_us": "https://logfire-us.pydantic.dev/mcp", "logfire_eu": "https://logfire-eu.pydantic.dev/mcp",
	"gmail": "https://gmailmcp.googleapis.com/mcp/v1", "drive": "https://drivemcp.googleapis.com/mcp/v1", "docs": "https://docsmcp.googleapis.com/mcp/v1", "sheets": "https://sheetsmcp.googleapis.com/mcp/v1", "slides": "https://slidesmcp.googleapis.com/mcp/v1", "calendar": "https://calendarmcp.googleapis.com/mcp/v1", "chat": "https://chatmcp.googleapis.com/mcp/v1", "people": "https://people.googleapis.com/mcp/v1",
}

func Hosted(service string, client *http.Client) (MCP, error) {
	url, ok := endpoints[service]
	if !ok {
		return MCP{}, fmt.Errorf("unknown MCP service %q", service)
	}
	return MCP{Prefix: service, IncludeInstructions: true, Transport: func(context.Context) (mcp.Transport, error) {
		return &mcp.StreamableClientTransport{Endpoint: url, HTTPClient: client}, nil
	}}, nil
}
