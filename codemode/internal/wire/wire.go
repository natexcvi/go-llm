// Package wire defines the request exchanged with the isolated interpreter.
package wire

import "encoding/json"

type Call struct {
	Name      string
	Arguments json.RawMessage
}
type Finished struct {
	Call   Call
	Result json.RawMessage
}
type Snippet struct {
	Code    string
	History []Finished
}
type Request struct {
	REPL     bool
	Previous []Snippet `json:",omitempty"`

	Params       json.RawMessage
	ValidateOnly bool
	Code         string
	Names        []string
	History      []Finished
}
type Response struct {
	REPL    bool `json:",omitempty"`
	Call    *Call
	Result  json.RawMessage
	Printed string
	Error   string
}
