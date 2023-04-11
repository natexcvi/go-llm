package tools

import (
	"bytes"
	"encoding/json"
	"fmt"

	"text/template"

	"golang.org/x/exp/maps"
)

type KeyValueStore struct {
	store map[string]string
}

type kvStoreArgs struct {
	Command string `json:"command"`
	Key     string `json:"key"`
	Value   string `json:"value"`
}

func (s *KeyValueStore) Execute(args json.RawMessage) (json.RawMessage, error) {
	var command kvStoreArgs
	err := json.Unmarshal(args, &command)
	if err != nil {
		return nil, err
	}
	switch command.Command {
	case "get":
		value, ok := s.store[command.Key]
		if !ok {
			return nil, fmt.Errorf("key not found: %s", command.Key)
		}
		return json.Marshal(value)
	case "set":
		s.store[command.Key] = command.Value
		return json.Marshal("stored successfully")
	case "list":
		keys := maps.Keys(s.store)
		return json.Marshal(keys)
	default:
		return nil, fmt.Errorf("unknown command: %s", command.Command)
	}
}

func (s *KeyValueStore) Name() string {
	return "store"
}

func (s *KeyValueStore) Description() string {
	return "A place where you can store any key-value pairs " +
		"of data. This is useful mainly for long values, which you should " +
		"store here to save memory. To use a value you have store, " +
		"reference it by Go template syntax: {{ store \"key\" }}. " +
		"where \"key\" is the key you used to store the value. " +
		"You can reference a saved value anywhere you want, including " +
		"arguments to other tools."
}

func (s *KeyValueStore) ArgsSchema() json.RawMessage {
	return json.RawMessage(`{"command": "either 'set', 'get' or 'list'", "key": "the key to store or retrieve. Specify only for 'get' and 'set'.", "value": "the value to store. Specify only for 'set'."}`)
}

func (s *KeyValueStore) CompactArgs(args json.RawMessage) json.RawMessage {
	var command kvStoreArgs
	err := json.Unmarshal(args, &command)
	if err != nil {
		return args
	}
	switch command.Command {
	case "set":
		return json.RawMessage(fmt.Sprintf(`{"command": "set", "key": "%s", "value": "<omitted>"}`, command.Key))
	default:
		return args
	}
}

func (s *KeyValueStore) Process(args json.RawMessage) (json.RawMessage, error) {
	tmpl := template.New("store").Funcs(template.FuncMap{
		"store": func(key string) string {
			value, ok := s.store[key]
			if !ok {
				return fmt.Sprintf("{{ store \"%s\" }}", key)
			}
			return value
		},
	})
	tmpl, err := tmpl.Parse(string(args))
	if err != nil {
		return nil, fmt.Errorf("error parsing args: %s", err)
	}
	var processedArgs bytes.Buffer
	err = tmpl.Execute(&processedArgs, nil)
	if err != nil {
		return nil, fmt.Errorf("error processing args: %s", err)
	}
	return processedArgs.Bytes(), nil
}

func NewKeyValueStore() *KeyValueStore {
	return &KeyValueStore{
		store: map[string]string{},
	}
}
