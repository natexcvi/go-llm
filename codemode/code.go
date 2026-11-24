// Package codemode runs small tool-using Starlark programs in isolated
// WebAssembly instances. Compile the guest from codemode/cmd/harness-code
// with GOOS=wasip1 GOARCH=wasm and pass the resulting module to Open.
package codemode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/codemode/internal/wire"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// Engine owns a compiled guest module. It can serve multiple agents and runs.
type Engine struct {
	authorMu sync.Mutex
	runtime  wazero.Runtime
	module   wazero.CompiledModule
}

func Open(ctx context.Context, guest []byte) (*Engine, error) {
	r := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithMemoryLimitPages(1024).WithCloseOnContextDone(true))
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, r); err != nil {
		r.Close(ctx)
		return nil, err
	}
	module, err := r.CompileModule(ctx, guest)
	if err != nil {
		r.Close(ctx)
		return nil, err
	}
	return &Engine{runtime: r, module: module}, nil
}
func (e *Engine) Close(ctx context.Context) error { return e.runtime.Close(ctx) }

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1<<20 {
		return 0, errors.New("program output exceeds 1 MiB")
	}
	return b.Buffer.Write(p)
}
func (e *Engine) evaluate(ctx context.Context, in wire.Request) (wire.Response, error) {
	var out wire.Response
	data, err := json.Marshal(in)
	if err != nil {
		return out, err
	}
	if len(data) > 4<<20 {
		return out, h.Retry(errors.New("program inputs exceed 4 MiB"))
	}
	var stdout limitedBuffer
	guestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conf := wazero.NewModuleConfig().WithName("").WithStdin(bytes.NewReader(data)).WithStdout(&stdout)
	m, err := e.runtime.InstantiateModule(guestCtx, e.module, conf)
	if m != nil {
		defer m.Close(ctx)
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if err != nil {
		return out, h.Retry(fmt.Errorf("program execution: %w", err))
	}
	if err = json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return out, h.Retry(fmt.Errorf("program result: %w", err))
	}
	if out.Error != "" {
		return out, h.Retry(errors.New(out.Error))
	}
	return out, nil
}

type codeInput struct {
	Code string `json:"code"`
}

var identifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// Capability offers the selected tools as functions accepting keyword arguments.
// The final expression and print output are returned to the model. A program
// may make up to 32 nested calls. Completed calls are journaled while awaiting
// approval, so resuming that program reuses their results. Bindings and functions
// persist across snippets. A restart call clears them.
func (e *Engine) Capability(tools ...h.Tool) h.Capability {
	return e.programCapability("run_code", tools, true)
}

// Stateless gives each run_code call its own Starlark environment. It also
// supports older guest modules built before REPL sessions were introduced.
func (e *Engine) Stateless(tools ...h.Tool) h.Capability {
	return e.programCapability("run_code", tools, false)
}

func (e *Engine) programCapability(name string, tools []h.Tool, repl bool) h.Capability {
	tools = slices.Clone(tools)
	return func(ctx context.Context, r *h.Run) error {
		if e == nil || e.module == nil {
			return errors.New("compiled code engine required")
		}
		catalog := map[string]h.Tool{}
		names := []string{}
		var signatures []string
		for _, t := range tools {
			if !identifier.MatchString(t.Name) || t.Name == name || t.Execute == nil || !json.Valid(t.Schema) {
				return fmt.Errorf("invalid program tool %q", t.Name)
			}
			if _, ok := catalog[t.Name]; ok {
				return fmt.Errorf("duplicate program tool %q", t.Name)
			}
			catalog[t.Name] = t
			names = append(names, t.Name)
			signatures = append(signatures, t.Name+" — "+t.Description+" "+string(t.Schema))
		}
		var previous []wire.Snippet
		if repl {
			if err := h.BindState(r, "code_mode.repl."+name, &previous); err != nil {
				return err
			}
		}
		var journal struct {
			Step    int
			ID      string
			Hash    [32]byte
			History []wire.Finished
		}
		if err := h.BindState(r, "code_mode."+name, &journal); err != nil {
			return err
		}
		description := "Execute a Starlark program. Use keyword arguments for tool calls, loops and comprehensions for processing, and a final expression for the result. Available functions:\n" + strings.Join(signatures, "\n")
		type replInput struct {
			Code    string `json:"code"`
			Restart bool   `json:"restart,omitempty"`
		}
		input := h.JSON[replInput]()
		if repl {
			description += " Bindings and functions persist between calls. Set restart to clear them."
		} else {
			simple := h.JSON[codeInput]()
			input.Schema = simple.Schema
			input.Decode = func(raw string) (replInput, error) { v, err := simple.Decode(raw); return replInput{Code: v.Code}, err }
		}
		run := h.Tool{Definition: h.Definition{Name: name, Description: description, Schema: input.Schema}}
		run.Execute = func(ctx context.Context, outer h.Call) (string, error) {
			args, err := input.Decode(outer.Arguments)
			if err != nil {
				return "", h.Retry(err)
			}
			if len(args.Code) > 64<<10 {
				return "", h.Retry(errors.New("code exceeds 64 KiB"))
			}
			hash := sha256.Sum256([]byte(args.Code))
			if journal.Step != r.Step || journal.ID != outer.ID || journal.Hash != hash {
				if args.Restart {
					previous = nil
				}
				journal.Step = r.Step
				journal.ID = outer.ID
				journal.Hash = hash
				journal.History = nil
			}
			if repl && len(previous) >= 64 {
				return "", h.Retry(errors.New("REPL holds 64 snippets; restart the session"))
			}
			for {
				step, err := e.evaluate(ctx, wire.Request{REPL: repl, Previous: previous, Code: args.Code, Names: names, History: journal.History})
				if err != nil {
					return "", err
				}
				if repl && !step.REPL {
					return "", errors.New("REPL requires a guest built with session support")
				}
				if step.Call == nil {
					if repl {
						previous = append(previous, wire.Snippet{Code: args.Code, History: journal.History})
					}
					journal.History = nil
					return strings.TrimSuffix(step.Printed+string(step.Result), "\n"), nil
				}
				if len(journal.History) >= 32 {
					return "", h.Retry(errors.New("program call limit exceeded"))
				}
				call := *step.Call
				t, ok := catalog[call.Name]
				if !ok {
					return "", errors.New("guest requested an unavailable function")
				}
				c := h.Call{Name: t.Name, ID: fmt.Sprintf("%s/%d", outer.ID, len(journal.History)+1), Arguments: string(call.Arguments)}
				value, err := r.InvokeTool(ctx, t, c)
				if err != nil {
					return "", err
				}
				raw := json.RawMessage(value)
				if t.TextResult || !json.Valid(raw) {
					raw, _ = json.Marshal(value)
				}
				journal.History = append(journal.History, wire.Finished{Call: call, Result: raw})
			}
		}
		return r.Register(run)
	}
}
