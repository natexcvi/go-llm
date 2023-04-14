package tools

import (
	"bytes"
	"encoding/json"
	"fmt"

	"text/template"

	"github.com/hashicorp/go-multierror"
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
	if command.Command == "" {
		if command.Key != "" && command.Value != "" {
			command.Command = "set"
		}
		if command.Key != "" && command.Value == "" {
			command.Command = "get"
		}
		if command.Key == "" && command.Value == "" {
			command.Command = "list"
		}
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
		"store here to save memory. To use a value you have stored, " +
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

func (s *KeyValueStore) recursivelyProcessStringFields(input any, processor func(string) string) any {
	switch input := input.(type) {
	case map[string]interface{}:
		output := map[string]interface{}{}
		for k, v := range input {
			output[k] = s.recursivelyProcessStringFields(v, processor)
		}
		return output
	case []interface{}:
		output := make([]interface{}, len(input))
		for i, v := range input {
			output[i] = s.recursivelyProcessStringFields(v, processor)
		}
		return output
	case string:
		return processor(input)
	default:
		return input
	}
}

func (s *KeyValueStore) Process(args json.RawMessage) (json.RawMessage, error) {
	tmpl := template.New("store").Funcs(template.FuncMap{
		"store": func(key string) string {
			value, ok := s.store[key]
			if !ok {
				return fmt.Sprintf("{{ store %q }}", key)
			}
			return value
		},
	})
	var unmarshaledArgs any
	err := json.Unmarshal(args, &unmarshaledArgs)
	if err != nil {
		return nil, fmt.Errorf("error unmarshaling args: %s", err)
	}
	var temlErr *multierror.Error
	processedArgs := s.recursivelyProcessStringFields(unmarshaledArgs, func(input string) string {
		tmpl, err := tmpl.Parse(input)
		if err != nil {
			temlErr = multierror.Append(temlErr, fmt.Errorf("error parsing args: %s", err))
			return input
		}
		var processedArgs bytes.Buffer
		err = tmpl.Execute(&processedArgs, nil)
		if err != nil {
			temlErr = multierror.Append(temlErr, fmt.Errorf("error processing args: %s", err))
			return input
		}
		return processedArgs.String()
	})
	if temlErr.ErrorOrNil() != nil {
		return nil, temlErr
	}
	var marshaledProcessedArgs json.RawMessage
	marshaledProcessedArgs, err = json.Marshal(processedArgs)
	if err != nil {
		return nil, fmt.Errorf("error marshaling processed args: %s", err)
	}
	return marshaledProcessedArgs, nil
}

func NewKeyValueStore() *KeyValueStore {
	return &KeyValueStore{
		store: map[string]string{},
	}
}
