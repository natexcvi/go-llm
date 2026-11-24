package capabilities

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	h "github.com/natexcvi/go-llm"
)

// Delegate describes one member of a named subagent roster. An unset model
// inherits the current parent model. MaxCalls counts completed delegations.
type Delegate struct {
	Name, Description string
	Agent             h.Agent[string]
	Models            []string
	MaxCalls          int
	Timeout           time.Duration
}
type SubAgents struct {
	Agents           []Delegate
	Models           map[string]h.Model
	MaxDepth         int // default 3
	IncludeSelf      bool
	IndependentUsage bool
	ForwardEvents    bool
}
type delegationDepth struct{}
type delegateFunc func(context.Context, string, string, h.Model) (string, error)

func bindDelegate(r *h.Run, name string, d Delegate, shared, events bool) (delegateFunc, error) {
	var state struct {
		Step     int
		CallID   string
		Snapshot *h.Snapshot
		Calls    int
	}
	if err := h.BindState(r, "child."+name, &state); err != nil {
		return nil, err
	}
	return func(ctx context.Context, id, task string, model h.Model) (string, error) {
		if d.MaxCalls > 0 && state.Calls >= d.MaxCalls {
			return "Delegation call limit reached for " + name, nil
		}
		child := d.Agent
		if model != nil {
			child.Model = model
		}
		if child.Model == nil {
			child.Model = r.Model
		}
		if child.Output.Decode == nil {
			child.Output = h.Text()
		}
		if d.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, d.Timeout)
			defer cancel()
		}
		opts, err := r.ChildOptions(ctx, name, shared, events)
		if err != nil {
			return "", err
		}
		var result h.Result[string]
		if state.Snapshot != nil && state.Step == r.Step && state.CallID == id {
			result, err = child.Resume(ctx, *state.Snapshot, opts)
		} else {
			result, err = child.Run(ctx, task, opts)
		}
		if errors.Is(err, h.ErrDeferred) {
			state.Step = r.Step
			state.CallID = id
			state.Snapshot = &result.Snapshot
		} else {
			state.Snapshot = nil
			state.Calls++
		}
		return result.Output, err
	}, nil
}

func (cfg SubAgents) Capability() h.Capability {
	cfg.Agents = slices.Clone(cfg.Agents)
	for i := range cfg.Agents {
		cfg.Agents[i].Models = slices.Clone(cfg.Agents[i].Models)
	}
	cfg.Models = maps.Clone(cfg.Models)
	return func(_ context.Context, r *h.Run) error {
		maxDepth := cfg.MaxDepth
		if maxDepth == 0 {
			maxDepth = 3
		}
		delegates := cfg.Agents
		if cfg.IncludeSelf {
			delegates = append(slices.Clone(delegates), Delegate{Name: "self", Description: "Handle a focused task in a new run of this agent.", Agent: r.SelfAgent()})
		}
		if maxDepth < 1 || len(delegates) == 0 {
			return errors.New("invalid subagent roster")
		}
		calls := map[string]delegateFunc{}
		allowed := map[string][]string{}
		var roster []string
		for _, d := range delegates {
			if d.Name == "" || calls[d.Name] != nil || d.MaxCalls < 0 || d.Timeout < 0 {
				return fmt.Errorf("invalid or duplicate delegate %q", d.Name)
			}
			for _, key := range d.Models {
				if cfg.Models[key] == nil {
					return fmt.Errorf("unknown model %q for delegate %q", key, d.Name)
				}
			}
			f, err := bindDelegate(r, d.Name, d, !cfg.IndependentUsage, cfg.ForwardEvents)
			if err != nil {
				return err
			}
			calls[d.Name] = f
			allowed[d.Name] = d.Models
			desc := d.Name + ": " + d.Description
			if len(d.Models) > 0 {
				desc += " (models: " + strings.Join(d.Models, ", ") + ")"
			}
			roster = append(roster, desc)
		}
		if len(cfg.Models) > 0 {
			keys := slices.Sorted(maps.Keys(cfg.Models))
			for _, k := range keys {
				if k == "" || cfg.Models[k] == nil {
					return errors.New("invalid delegate model menu")
				}
			}
			roster = append(roster, "Available models: "+strings.Join(keys, ", "))
		}
		r.Instructions = append(r.Instructions, "Available delegates:\n"+strings.Join(roster, "\n"))
		var id string
		run := func(ctx context.Context, name, task, key string) (string, error) {
			f := calls[name]
			if f == nil {
				return "", h.Retry(fmt.Errorf("unknown delegate %q", name))
			}
			depth, _ := ctx.Value(delegationDepth{}).(int)
			if depth+1 >= maxDepth {
				return "Maximum delegation depth reached.", nil
			}
			menu := allowed[name]
			if key == "" && len(menu) > 0 {
				key = menu[0]
			}
			if key != "" && (cfg.Models[key] == nil || len(menu) > 0 && !slices.Contains(menu, key)) {
				return "", h.Retry(fmt.Errorf("unavailable model %q for %q", key, name))
			}
			return f(context.WithValue(ctx, delegationDepth{}, depth+1), id, task, cfg.Models[key])
		}
		var t h.Tool
		if len(cfg.Models) == 0 {
			t = h.NewTool("delegate_task", "Send a task to a named delegate.", func(ctx context.Context, in struct {
				Name string `json:"agent_name"`
				Task string `json:"task"`
			}) (string, error) {
				return run(ctx, in.Name, in.Task, "")
			})
		} else {
			t = h.NewTool("delegate_task", "Send a task to a named delegate and optionally select its model.", func(ctx context.Context, in struct {
				Name  string `json:"agent_name"`
				Task  string `json:"task"`
				Model string `json:"model,omitempty"`
			}) (string, error) {
				return run(ctx, in.Name, in.Task, in.Model)
			})
		}
		execute := t.Execute
		t.Execute = func(ctx context.Context, c h.Call) (string, error) { id = c.ID; return execute(ctx, c) }
		return r.Register(t)
	}
}
