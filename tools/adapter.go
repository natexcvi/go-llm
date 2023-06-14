package tools

import (
	"encoding/json"
	"fmt"

	"github.com/natexcvi/go-llm/engines"
)

var (
	ErrCannotAutoConvertArgSchema = fmt.Errorf("cannot auto-convert arg schema")
)

func ConvertToNativeFunctionSpecs(tool Tool) (engines.FunctionSpecs, error) {
	parameterSpecs, err := convertArgSchemaToParameterSpecs(tool.ArgsSchema())
	if err != nil {
		return engines.FunctionSpecs{}, err
	}
	return engines.FunctionSpecs{
		Name:        tool.Name(),
		Description: tool.Description(),
		Parameters:  &parameterSpecs,
	}, nil
}

func convertArgSchemaToParameterSpecs(argSchema json.RawMessage) (engines.ParameterSpecs, error) {
	var unmarshaledSchema any
	if err := json.Unmarshal(argSchema, &unmarshaledSchema); err != nil {
		return engines.ParameterSpecs{}, err
	}
	switch schema := unmarshaledSchema.(type) {
	case map[string]any:
		specs := engines.ParameterSpecs{
			Type:       "object",
			Properties: map[string]*engines.ParameterSpecs{},
			Required:   []string{},
		}
		for key, value := range schema {
			marshaledValue, err := json.Marshal(value)
			if err != nil {
				return engines.ParameterSpecs{}, err
			}
			propertySpecs, err := convertArgSchemaToParameterSpecs(marshaledValue)
			if err != nil {
				return engines.ParameterSpecs{}, err
			}
			specs.Properties[key] = &propertySpecs
			// specs.Required = append(specs.Required, key)
		}
		return specs, nil
	case []any:
		specs := engines.ParameterSpecs{
			Type:  "array",
			Items: nil,
		}
		// infer type from first element
		if len(schema) > 0 {
			marshaledValue, err := json.Marshal(schema[0])
			if err != nil {
				return engines.ParameterSpecs{}, err
			}
			propertySpecs, err := convertArgSchemaToParameterSpecs(marshaledValue)
			if err != nil {
				return engines.ParameterSpecs{}, err
			}
			specs.Items = &propertySpecs
		}
		return specs, nil
	case string:
		return engines.ParameterSpecs{
			Type:        "string",
			Description: schema,
		}, nil
	case float64, int:
		return engines.ParameterSpecs{
			Type:        "number",
			Description: "a number",
		}, nil
	case bool:
		return engines.ParameterSpecs{
			Type:        "boolean",
			Description: "a boolean value",
		}, nil
	default:
		return engines.ParameterSpecs{}, ErrCannotAutoConvertArgSchema
	}
}
