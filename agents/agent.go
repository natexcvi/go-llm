package agents

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/hashicorp/go-multierror"
	"github.com/natexcvi/go-llm/engines"
	"github.com/natexcvi/go-llm/memory"
	"github.com/natexcvi/go-llm/tools"
	"github.com/samber/lo"
	"golang.org/x/exp/maps"
)

const (
	ThoughtCode     = "THT"
	ActionCode      = "ACT"
	AnswerCode      = "ANS"
	ErrorCode       = "ERR"
	ObservationCode = "OBS"
	MessageFormat   = "%s: %s"
	MessagePrefix   = "%s: "
)

var (
	actionRegex    = regexp.MustCompile(`^(?P<tool>.*?)\((?P<args>[\s\S]*)\)`)
	operationRegex = regexp.MustCompile(`^(?P<code>[A-Z]{3}): (?P<content>.*)`)
)

//go:generate mockgen -source=agent.go -destination=mocks/agent.go -package=mocks
type Agent[T any, S any] interface {
	Run(input T) (S, error)
}

type ChainAgentThought struct {
	Content string
}

func (a *ChainAgentThought) Encode() string {
	return fmt.Sprintf(MessageFormat, ThoughtCode, a.Content)
}

func ParseChainAgentThought(thought *engines.ChatMessage) *ChainAgentThought {
	return &ChainAgentThought{
		Content: strings.TrimPrefix(thought.Text, fmt.Sprintf(MessagePrefix, ThoughtCode)),
	}
}

type ChainAgentAction struct {
	Tool tools.Tool
	Args json.RawMessage
}

func (a *ChainAgentAction) Encode() string {
	return fmt.Sprintf(MessageFormat, ActionCode, fmt.Sprintf("%s(%s)", a.Tool.Name(), a.Args))
}

func ParseChainAgentAction(msg *engines.ChatMessage, tools map[string]tools.Tool) (*ChainAgentAction, error) {
	matches := actionRegex.FindStringSubmatch(msg.Text)
	if len(matches) != 3 {
		return nil, fmt.Errorf("invalid action format: message must start with `%s: ` and the action call itself must match regex %q", ActionCode, actionRegex.String())
	}
	toolName := matches[actionRegex.SubexpIndex("tool")]
	toolArgs := matches[actionRegex.SubexpIndex("args")]

	tool, ok := tools[toolName]
	if !ok {
		return nil, fmt.Errorf("tool not found. Available tools: %s", strings.Join(maps.Keys(tools), ", "))
	}

	return &ChainAgentAction{
		Tool: tool,
		Args: json.RawMessage(toolArgs),
	}, nil
}

type ChainAgentAnswer[S any] struct {
	Content S
}

func (agent *ChainAgent[T, S]) parseChainAgentAnswer(answer *engines.ChatMessage) (*ChainAgentAnswer[S], error) {
	output, err := agent.Task.AnswerParser(answer.Text)
	if err != nil {
		return nil, err
	}
	return &ChainAgentAnswer[S]{
		Content: output,
	}, nil
}

type ChainAgent[T json.Marshaler, S any] struct {
	Engine              engines.LLM
	Task                *Task[T, S]
	Tools               map[string]tools.Tool
	InputValidators     []func(T) error
	OutputValidators    []func(S) error
	MaxSolutionAttempts int
	Memory              memory.Memory
}

type ChainAgentObservation struct {
	Content string
}

func (a *ChainAgentObservation) Encode() string {
	return fmt.Sprintf(MessageFormat, ObservationCode, a.Content)
}

func (agent *ChainAgent[T, S]) executeAction(action *ChainAgentAction) (obs *engines.ChatMessage) {
	actionOutput, err := action.Tool.Execute(action.Args)
	if err != nil {
		log.Debugf("action error: %s", err.Error())
		return &engines.ChatMessage{
			Role: engines.ConvRoleSystem,
			Text: fmt.Sprintf(MessageFormat, ErrorCode, err.Error()),
		}
	}
	log.Debugf("action output: %s", actionOutput)
	return &engines.ChatMessage{
		Role: engines.ConvRoleSystem,
		Text: fmt.Sprintf(MessageFormat, ObservationCode, actionOutput),
	}
}

func (agent *ChainAgent[T, S]) parseResponse(response *engines.ChatMessage) (nextMessages []*engines.ChatMessage, answer *ChainAgentAnswer[S]) {
	ops := operationRegex.FindAllStringSubmatch(response.Text, -1)
	if len(ops) == 0 {
		nextMessages = append(nextMessages, &engines.ChatMessage{
			Role: engines.ConvRoleSystem,
			Text: fmt.Sprintf(MessageFormat, ErrorCode, "invalid response format: must have special operations matching regex "+operationRegex.String()),
		})
		return
	}
	for _, op := range ops {
		opCode := op[operationRegex.SubexpIndex("code")]
		opContent := op[operationRegex.SubexpIndex("content")]
		switch opCode {
		case ThoughtCode:
			thought := ParseChainAgentThought(&engines.ChatMessage{
				Role: engines.ConvRoleAssistant,
				Text: opContent,
			})
			nextMessages = append(nextMessages, &engines.ChatMessage{
				Role: engines.ConvRoleAssistant,
				Text: fmt.Sprintf(MessageFormat, ThoughtCode, thought.Content),
			})
		case ActionCode:
			action, err := ParseChainAgentAction(&engines.ChatMessage{
				Role: engines.ConvRoleAssistant,
				Text: opContent,
			}, agent.Tools)
			if err != nil {
				nextMessages = append(nextMessages, &engines.ChatMessage{
					Role: engines.ConvRoleSystem,
					Text: fmt.Sprintf(MessageFormat, ErrorCode, err.Error()),
				})
				break
			}
			obs := agent.executeAction(action)
			nextMessages = append(nextMessages, obs)
		case AnswerCode:
			answer, err := agent.parseChainAgentAnswer(&engines.ChatMessage{
				Role: engines.ConvRoleAssistant,
				Text: opContent,
			})
			if err != nil {
				nextMessages = append(nextMessages, &engines.ChatMessage{
					Role: engines.ConvRoleSystem,
					Text: fmt.Sprintf(MessageFormat, ErrorCode, err.Error()),
				})
				break
			}
			err = agent.validateAnswer(answer.Content)
			if err != nil {
				nextMessages = append(nextMessages, &engines.ChatMessage{
					Role: engines.ConvRoleSystem,
					Text: fmt.Sprintf(MessageFormat, ErrorCode, err.Error()),
				})
				break
			}
			return nextMessages, answer
		default:
			nextMessages = append(nextMessages, &engines.ChatMessage{
				Role: engines.ConvRoleSystem,
				Text: fmt.Sprintf(MessageFormat, ErrorCode, "invalid response: must begin with `THT`, `ACT`, or `ANS`."),
			})
		}
	}
	return nextMessages, nil
}

func (agent *ChainAgent[T, S]) validateAnswer(answer S) error {
	var answerErr *multierror.Error
	for _, validator := range agent.OutputValidators {
		answerErr = multierror.Append(answerErr, validator(answer))
	}
	return answerErr.ErrorOrNil()
}

func (agent *ChainAgent[T, S]) Run(input T) (output S, err error) {
	var inputErr *multierror.Error
	for _, validator := range agent.InputValidators {
		inputErr = multierror.Append(inputErr, validator(input))
	}
	if inputErr.ErrorOrNil() != nil {
		return output, fmt.Errorf("invalid input: %w", inputErr)
	}
	taskPrompt := agent.Task.Compile(input, agent.Tools)
	log.Debugf("task prompt: %+v", lo.Map(taskPrompt.History, func(m *engines.ChatMessage, _ int) string { return fmt.Sprintf("%+v", m) }))
	err = agent.Memory.AddPrompt(taskPrompt)
	if err != nil {
		return output, fmt.Errorf("failed to add prompt to memory: %w", err)
	}
	response, err := agent.Engine.Predict(taskPrompt)
	if err != nil {
		return output, fmt.Errorf("failed to predict response: %w", err)
	}
	err = agent.Memory.Add(response)
	if err != nil {
		return output, fmt.Errorf("failed to add response to memory: %w", err)
	}
	stepsExecuted := 0
	for {
		nextMessages, answer := agent.parseResponse(response)
		log.Debugf("next messages: %+v", lo.Map(nextMessages, func(m *engines.ChatMessage, _ int) string { return fmt.Sprintf("%+v", m) }))
		if answer != nil {
			return answer.Content, nil
		}
		prompt, err := agent.Memory.PromptWithContext(nextMessages...)
		if err != nil {
			return output, fmt.Errorf("failed to generate prompt: %w", err)
		}
		if agent.MaxSolutionAttempts > 0 && stepsExecuted > agent.MaxSolutionAttempts {
			return output, errors.New("max solution attempts reached")
		}
		response, err = agent.Engine.Predict(prompt)
		if err != nil {
			return output, fmt.Errorf("failed to predict response: %w", err)
		}
		log.Debugf("model response: %s", response.Text)
		err = agent.Memory.Add(response)
		if err != nil {
			return output, fmt.Errorf("failed to add response to memory: %w", err)
		}
		stepsExecuted++
	}
}

func NewChainAgent[T json.Marshaler, S any](engine engines.LLM, task *Task[T, S], memory memory.Memory) *ChainAgent[T, S] {
	return &ChainAgent[T, S]{
		Engine: engine,
		Task:   task,
		Tools:  map[string]tools.Tool{},
		Memory: memory,
	}
}

func (agent *ChainAgent[T, S]) WithTools(tools ...tools.Tool) *ChainAgent[T, S] {
	for _, tool := range tools {
		agent.Tools[tool.Name()] = tool
	}
	return agent
}

func (agent *ChainAgent[T, S]) WithInputValidators(validators ...func(T) error) *ChainAgent[T, S] {
	agent.InputValidators = append(agent.InputValidators, validators...)
	return agent
}

func (agent *ChainAgent[T, S]) WithOutputValidators(validators ...func(S) error) *ChainAgent[T, S] {
	agent.OutputValidators = append(agent.OutputValidators, validators...)
	return agent
}

func (agent *ChainAgent[T, S]) WithMaxSolutionAttempts(max int) *ChainAgent[T, S] {
	agent.MaxSolutionAttempts = max
	return agent
}
