package tools

import (
	"encoding/json"
)

type GenericTool struct {
	name        string
	description string
	argSchema   json.RawMessage
	handler     func(args json.RawMessage) (json.RawMessage, error)
}

func (b *GenericTool) Execute(args json.RawMessage) (json.RawMessage, error) {
	return b.handler(args)
}

func (b *GenericTool) Name() string {
	return b.name
}

func (b *GenericTool) Description() string {
	return b.description
}

func (b *GenericTool) ArgsSchema() json.RawMessage {
	return b.argSchema
}

func (b *GenericTool) CompactArgs(args json.RawMessage) json.RawMessage {
	return args
}

func NewGenericTool(name, description string, argSchema json.RawMessage, handler func(args json.RawMessage) (json.RawMessage, error)) *GenericTool {
	return &GenericTool{
		name:        name,
		description: description,
		argSchema:   argSchema,
		handler:     handler,
	}
}
