package harness

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

type bridgeCall struct {
	Name string `json:"name"`
	Args string `json:"args_json"`
}

// ToolBridge publishes a fixed search/describe/call interface for the tools in
// wrapped. Authorization and execution middleware receive the underlying call.
func ToolBridge(wrapped ...Capability) Capability {
	return func(ctx context.Context, r *Run) error {
		start := len(r.defs)
		if err := Combine(wrapped...)(ctx, r); err != nil {
			return err
		}
		catalog := slices.Clone(r.defs[start:])
		r.defs = r.defs[:start]
		defs := map[string]Definition{}
		for _, d := range catalog {
			defs[d.Name] = d
		}
		type summary struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		search := NewTool("tool_search", "Search the available tools by name and description.", func(_ context.Context, in struct {
			Queries []string `json:"queries"`
		}) ([]summary, error) {
			matches := []summary{}
			for _, d := range catalog {
				text := strings.ToLower(d.Name + " " + d.Description)
				for _, q := range in.Queries {
					if strings.Contains(text, strings.ToLower(q)) {
						matches = append(matches, summary{d.Name, d.Description})
						break
					}
				}
				if len(matches) == 20 {
					break
				}
			}
			return matches, nil
		})
		describe := NewTool("tool_describe", "Show the input schema for a tool.", func(_ context.Context, in struct {
			Name string `json:"name"`
		}) (Definition, error) {
			d, ok := defs[in.Name]
			if !ok {
				return Definition{}, Retry(fmt.Errorf("unknown tool %q", in.Name))
			}
			return d, nil
		})
		shape := JSON[bridgeCall]()
		bridge := Tool{Definition: Definition{Name: "tool_call", Description: "Call a catalog tool by name with its JSON argument string.", Schema: shape.Schema}, Execute: func(context.Context, Call) (string, error) {
			return "", Retry(errors.New("specify a known tool name and args_json"))
		}}
		r.PrepareCall = append(r.PrepareCall, func(c Call) (Call, error) {
			if c.Name != "tool_call" {
				return c, nil
			}
			in, err := shape.Decode(c.Arguments)
			if err != nil {
				return c, nil
			}
			if _, ok := defs[in.Name]; ok {
				c.Name = in.Name
				c.Arguments = in.Args
			}
			return c, nil
		})
		return r.Register(search, describe, bridge)
	}
}
