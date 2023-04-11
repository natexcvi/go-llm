package tools

import "encoding/json"

//go:generate mockgen -source=tool.go -destination=mocks/tool.go -package=mocks
type Tool interface {
	// Executes the tool with the given
	// arguments. If the arguments are
	// invalid, an error should be returned
	// which will be displayed to the agent.
	Execute(args json.RawMessage) (json.RawMessage, error)
	// The name of the tool, as it will be
	// displayed to the agent.
	Name() string
	// A short description of the tool, as
	// it will be displayed to the agent.
	Description() string
	// A 'fuzzy schema' of the arguments
	// that the tool expects. This is used
	// to instruct the agent on how to
	// generate the arguments.
	ArgsSchema() json.RawMessage
	// Generates a compact representation
	// of the arguments, to be used in the
	// agent's memory.
	CompactArgs(args json.RawMessage) json.RawMessage
}

type PreprocessingTool interface {
	// Preprocesses the arguments before
	// they are passed to any tool.
	Process(args json.RawMessage) (json.RawMessage, error)
}
