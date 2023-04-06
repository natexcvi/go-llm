package agents

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/invopop/jsonschema"
	"github.com/natexcvi/go-llm/engines"
	"github.com/natexcvi/go-llm/tools"
)

type Example[T json.Marshaler, S any] struct {
	Input             T
	IntermediarySteps []*engines.ChatMessage
	Answer            S
}

type Task[T json.Marshaler, S any] struct {
	Description  string
	Examples     []Example[T, S]
	AnswerParser func(string) (S, error)
}

func (task *Task[T, S]) Compile(input T, tools map[string]tools.Tool) *engines.ChatPrompt {
	inputSchema := jsonschema.Reflect(input)
	marshaledInputSchema, err := inputSchema.MarshalJSON()
	if err != nil {
		panic(err)
	}
	prompt := &engines.ChatPrompt{
		History: []*engines.ChatMessage{
			{
				Role: engines.ConvRoleSystem,
				Text: fmt.Sprintf("You are a smart, autonomous agent given the task below. "+
					"You will be given input from the user in the following format "+
					"(provided as a JSON schema): %s. Complete the task step-by-step, "+
					"reasoning about your solution steps by sending a message beginning "+
					"with `%s: `.\n\nTask description: %s",
					marshaledInputSchema, ThoughtCode, task.Description),
			},
		},
	}
	task.enrichPromptWithTools(tools, prompt)
	task.enrichPromptWithExamples(prompt)
	prompt.History = append(prompt.History, &engines.ChatMessage{
		Role: engines.ConvRoleSystem,
		Text: fmt.Sprintf("Now, you will be given the input. "+
			"It's very important that every message you send begins with either "+
			"`%s: `, `%s: `, or `%s: `, as was explained to you.", ThoughtCode, ActionCode, AnswerCode),
	})
	marshalledInput, err := input.MarshalJSON()
	if err != nil {
		panic(err)
	}
	prompt.History = append(prompt.History, &engines.ChatMessage{
		Role: engines.ConvRoleUser,
		Text: string(marshalledInput),
	})
	return prompt
}

func (task *Task[T, S]) enrichPromptWithExamples(prompt *engines.ChatPrompt) {
	prompt.History = append(prompt.History, &engines.ChatMessage{
		Role: engines.ConvRoleSystem,
		Text: "Here are some examples of how you might solve this task:",
	})
	for _, example := range task.Examples {
		marshalledInput, err := example.Input.MarshalJSON()
		if err != nil {
			panic(err)
		}
		prompt.History = append(prompt.History, &engines.ChatMessage{
			Role: engines.ConvRoleUser,
			Text: string(marshalledInput),
		})
		for _, step := range example.IntermediarySteps {
			prompt.History = append(prompt.History, step)
		}
		marshalledAnswer, err := json.Marshal(example.Answer)
		prompt.History = append(prompt.History, &engines.ChatMessage{
			Role: engines.ConvRoleSystem,
			Text: fmt.Sprintf(MessageFormat, AnswerCode, string(marshalledAnswer)),
		})
	}
}

func (*Task[T, S]) enrichPromptWithTools(tools map[string]tools.Tool, prompt *engines.ChatPrompt) {
	if len(tools) > 0 {
		toolsList := make([]string, 0, len(tools))
		for name, tool := range tools {
			toolsList = append(toolsList, fmt.Sprintf("%s(%s) # %s", name, tool.ArgsSchema(), tool.Description()))
		}
		prompt.History = append(prompt.History, &engines.ChatMessage{
			Role: engines.ConvRoleSystem,
			Text: fmt.Sprintf("Here are some tools you can use. To use a tool, "+
				"send a message in the form of `%s: tool_name(args)`, "+
				"where `args` is a valid JSON representation of the arguments"+
				" to the tool, as specified for it (using JSON schema). You will get "+
				"the output in "+
				"a message beginning with `%s: `, or an error message beginning "+
				"with `%s: `.\n\nTools:\n%s",
				ActionCode, ObservationCode, ErrorCode, strings.Join(toolsList, "\n")),
		})
	}
}
