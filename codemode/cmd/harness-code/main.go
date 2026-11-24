// Command harness-code is the isolated guest interpreter for package codemode.
// Build with GOOS=wasip1 GOARCH=wasm. The host supplies only standard input
// and output, a bounded linear memory, and a cancellable execution deadline.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/natexcvi/go-llm/codemode/internal/wire"
	starjson "go.starlark.net/lib/json"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

func encode(t *starlark.Thread, v starlark.Value) (json.RawMessage, error) {
	value, err := starlark.Call(t, starjson.Module.Members["encode"], starlark.Tuple{v}, nil)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(value.(starlark.String).GoString()), nil
}
func evaluate(in wire.Request) (out wire.Response, err error) {
	out.REPL = in.REPL
	thread := &starlark.Thread{Name: "run_code"}
	thread.SetMaxExecutionSteps(1000000)
	var printed strings.Builder
	thread.Print = func(t *starlark.Thread, s string) {
		if printed.Len()+len(s) > 64000 {
			t.Cancel("print output limit exceeded")
			return
		}
		printed.WriteString(s)
		printed.WriteByte('\n')
	}
	index := 0
	history := in.History
	restoring := false
	env := starlark.StringDict{}
	if in.Params != nil {
		v, err := starlark.Call(thread, starjson.Module.Members["decode"], starlark.Tuple{starlark.String(in.Params)}, nil)
		if err != nil {
			return out, err
		}
		env["params"] = v
	}
	for _, name := range in.Names {
		env[name] = starlark.NewBuiltin(name, func(t *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
			if len(args) > 0 {
				return nil, errors.New("tool calls require keyword arguments")
			}
			object := starlark.NewDict(len(kwargs))
			for _, pair := range kwargs {
				if err := object.SetKey(pair[0], pair[1]); err != nil {
					return nil, err
				}
			}
			raw, err := encode(t, object)
			if err != nil {
				return nil, err
			}
			// The wire encoder normalizes JSON whitespace and HTML escaping.
			normalized, err := json.Marshal(raw)
			if err != nil {
				return nil, err
			}
			call := wire.Call{Name: b.Name(), Arguments: normalized}
			if index == len(history) {
				if restoring {
					return nil, errors.New("completed snippet requested an unrecorded tool")
				}
				out.Call = &call
				return nil, errors.New("await tool")
			}
			done := history[index]
			index++
			if call.Name != done.Call.Name || !bytes.Equal(call.Arguments, done.Call.Arguments) {
				return nil, errors.New("program diverged during replay")
			}
			return starlark.Call(t, starjson.Module.Members["decode"], starlark.Tuple{starlark.String(done.Result)}, nil)
		})
	}
	const result = "__harness_code_result__"
	parse := func(code string) (*syntax.File, error) {
		options := syntax.FileOptions{TopLevelControl: true, GlobalReassign: true}
		f, err := options.Parse("program.star", code, 0)
		if err != nil {
			return nil, err
		}
		if n := len(f.Stmts); n > 0 {
			if final, ok := f.Stmts[n-1].(*syntax.ExprStmt); ok {
				pos, _ := final.Span()
				f.Stmts[n-1] = &syntax.AssignStmt{Op: syntax.EQ, OpPos: pos, LHS: &syntax.Ident{Name: result, NamePos: pos}, RHS: final.X}
			}
		}
		return f, nil
	}
	f, err := parse(in.Code)
	if err != nil {
		return out, err
	}
	var globals starlark.StringDict
	if in.REPL {
		globals = env
		restoring = true
		for _, prior := range in.Previous {
			old, err := parse(prior.Code)
			if err != nil {
				return out, err
			}
			history, index = prior.History, 0
			globals[result] = starlark.None
			if err := starlark.ExecREPLChunk(old, thread, globals); err != nil {
				return out, err
			}
			if index != len(history) {
				return out, errors.New("incomplete snippet replay")
			}
			printed.Reset()
		}
		history, index, restoring = in.History, 0, false
		globals[result] = starlark.None
		err = starlark.ExecREPLChunk(f, thread, globals)
	} else {
		program, err := starlark.FileProgram(f, env.Has)
		if err != nil {
			return out, err
		}
		if in.ValidateOnly {
			return out, nil
		}
		var runErr error
		globals, runErr = program.Init(thread, env)
		if runErr != nil && out.Call == nil {
			return out, runErr
		}
	}
	if out.Call != nil {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if index != len(in.History) {
		return out, errors.New("program ended before replay was complete")
	}
	out.Printed = printed.String()
	if value := globals[result]; value != nil && value != starlark.None {
		out.Result, err = encode(thread, value)
	}
	return out, err
}
func main() {
	var in wire.Request
	var out wire.Response
	err := json.NewDecoder(os.Stdin).Decode(&in)
	if err == nil {
		out, err = evaluate(in)
	}
	if err != nil {
		out = wire.Response{Error: err.Error()}
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
