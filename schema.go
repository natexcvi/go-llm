package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"

	reflectschema "github.com/invopop/jsonschema"
	validator "github.com/santhosh-tekuri/jsonschema/v6"
)

// Output defines how a final response becomes an application value.
type Output[T any] struct {
	Schema json.RawMessage
	Decode func(string) (T, error)
}

func Text() Output[string] {
	return Output[string]{Decode: func(s string) (string, error) { return s, nil }}
}

// JSON derives a JSON Schema from T, validates required fields and constraints,
// and decodes the validated value. Struct fields use their standard json tags.
func JSON[T any]() Output[T] {
	typ := reflect.TypeFor[T]()
	cached, _ := schemas.LoadOrStore(typ, sync.OnceValue(func() Output[T] { return jsonOutput[T](typ) }))
	output := cached.(func() Output[T])()
	output.Schema = slices.Clone(output.Schema)
	return output
}

var schemas sync.Map

func jsonOutput[T any](typ reflect.Type) Output[T] {
	r := reflectschema.Reflector{Anonymous: true}
	root := r.ReflectFromType(typ)
	// Inline the top-level object for providers that require type:object.
	// Keep its definition available for recursive references.
	if key, ok := strings.CutPrefix(root.Ref, "#/$defs/"); ok && root.Definitions[key] != nil {
		expanded := *root.Definitions[key]
		expanded.Definitions = root.Definitions
		root = &expanded
	}
	raw, err := json.Marshal(root)
	var schema *validator.Schema
	if err == nil {
		var doc any
		err = json.Unmarshal(raw, &doc)
		if err == nil {
			c := validator.NewCompiler()
			const name = "urn:go-llm:output"
			err = c.AddResource(name, doc)
			if err == nil {
				schema, err = c.Compile(name)
			}
		}
	}
	return Output[T]{Schema: raw, Decode: func(s string) (T, error) {
		var value T
		if err != nil {
			return value, err
		}
		tree, e := validator.UnmarshalJSON(strings.NewReader(s))
		if e != nil {
			return value, e
		}
		if e = schema.Validate(tree); e != nil {
			return value, e
		}
		e = json.Unmarshal([]byte(s), &value)
		return value, e
	}}
}

type Tool struct {
	Definition
	// TextResult preserves string output when embedding the tool in a program.
	TextResult bool
	Execute    func(context.Context, Call) (string, error)
}

// NewTool binds a typed Go function. Invalid arguments request a model correction.
// String results are returned verbatim and other values are JSON encoded.
func NewTool[I, O any](name, description string, fn func(context.Context, I) (O, error)) Tool {
	input := JSON[I]()
	return Tool{Definition: Definition{name, description, input.Schema}, TextResult: reflect.TypeFor[O]() == reflect.TypeFor[string](), Execute: func(ctx context.Context, call Call) (string, error) {
		if fn == nil {
			return "", errors.New("nil tool handler")
		}
		args, err := input.Decode(call.Arguments)
		if err != nil {
			return "", Retry(fmt.Errorf("invalid arguments for %s: %w", name, err))
		}
		out, err := fn(ctx, args)
		if err != nil {
			return "", err
		}
		if s, ok := any(out).(string); ok {
			return s, nil
		}
		b, err := json.Marshal(out)
		return string(b), err
	}}
}
