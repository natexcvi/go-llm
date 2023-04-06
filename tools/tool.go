package tools

import "encoding/json"

//go:generate mockgen -source=tool.go -destination=mocks/tool.go -package=mocks
type Tool interface {
	Execute(args json.RawMessage) (json.RawMessage, error)
	Name() string
	Description() string
	ArgsSchema() json.RawMessage
}
