package agents

import (
	"fmt"
	"strings"

	"github.com/natexcvi/go-llm/engines"
	"github.com/natexcvi/go-llm/tools"
	"github.com/samber/lo"
)

type Representable interface {
	Encode() string
	Schema() string
}

type Example[T Representable, S Representable] struct {
	Input             T
	IntermediarySteps []*engines.ChatMessage
	Answer            S
}

type Task[T Representable, S Representable] struct {
	Description  string
	Examples     []Example[T, S]
	AnswerParser func(string) (S, error)
}

func (task *Task[T, S]) Compile(input T, tools map[string]tools.Tool) *engines.ChatPrompt {
	answerSchema := lo.IfF(
		task.Examples != nil && len(task.Examples) > 0,
		func() string { return task.Examples[0].Answer.Schema() },
	).Else("")

	prompt := &engines.ChatPrompt{
		History: []*engines.ChatMessage{
			{
				Role: engines.ConvRoleSystem,
				Text: fmt.Sprintf("You are a smart, autonomous agent given the task below. "+
					"You will be given input from the user in the following format:\n\n"+
					"%s\n\n Complete the task step-by-step, "+
					"reasoning about your solution steps by sending thought messages in format "+
					"`%s: (your reflection)%s`. When you are ready to return your response, "+
					"send an answer message in format `%s: %s%s`. Remember: you are on your own - "+
					"do not ask for any clarifications, except by using appropriate tools "+
					"that enable interaction with the user. You should determine when you are "+
					"done with the task, and return your answer.",
					input.Schema(), ThoughtCode, EndMarker, AnswerCode, answerSchema, EndMarker),
			},
		},
	}
	task.enrichPromptWithTools(tools, prompt)
	prompt.History = append(prompt.History, &engines.ChatMessage{
		Role: engines.ConvRoleUser,
		Text: task.Description,
	})
	task.enrichPromptWithExamples(prompt)
	prompt.History = append(prompt.History, &engines.ChatMessage{
		Role: engines.ConvRoleUser,
		Text: input.Encode(),
	})
	return prompt
}

func (task *Task[T, S]) enrichPromptWithExamples(prompt *engines.ChatPrompt) {
	if len(task.Examples) == 0 {
		return
	}
	for _, example := range task.Examples {
		prompt.History = append(prompt.History, &engines.ChatMessage{
			Role: engines.ConvRoleUser,
			Text: example.Input.Encode(),
		})
		for _, step := range example.IntermediarySteps {
			prompt.History = append(prompt.History, step)
		}
		answerRepresentation := example.Answer.Encode()
		prompt.History = append(prompt.History, &engines.ChatMessage{
			Role: engines.ConvRoleAssistant,
			Text: fmt.Sprintf(MessageFormat, AnswerCode, answerRepresentation),
		})
	}
}

func (*Task[T, S]) enrichPromptWithTools(tools map[string]tools.Tool, prompt *engines.ChatPrompt) {
	if len(tools) == 0 {
		return
	}
	toolsList := make([]string, 0, len(tools))
	for name, tool := range tools {
		toolsList = append(toolsList, fmt.Sprintf("%s(%s) # %s", name, tool.ArgsSchema(), tool.Description()))
	}
	prompt.History = append(prompt.History, &engines.ChatMessage{
		Role: engines.ConvRoleSystem,
		Text: fmt.Sprintf("Here are some tools you can use. To use a tool, "+
			"send a message in the form of `%s: tool_name(args)%s`, "+
			"where `args` is a valid one-line JSON representation of the arguments"+
			" to the tool, as specified for it. You will get "+
			"the output in "+
			"a message beginning with `%s: `, or an error message beginning "+
			"with `%s: `.\n\nTools:\n%s",
			ActionCode, EndMarker, ObservationCode, ErrorCode, strings.Join(toolsList, "\n")),
	})
}
