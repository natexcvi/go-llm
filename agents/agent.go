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
	toolsPkg "github.com/natexcvi/go-llm/tools"
	"golang.org/x/exp/maps"
)

const (
	ThoughtCode     = "Thought"
	ActionCode      = "Action"
	AnswerCode      = "Answer"
	ErrorCode       = "Error"
	ObservationCode = "Observation"
	EndMarker       = "<END>"
	MessageFormat   = "%s: %s<END>"
	MessagePrefix   = "%s: "
)

var (
	actionRegex              = regexp.MustCompile(`^(?P<tool>.*?)\((?P<args>[\s\S]*)\)`)
	operationRegex           = regexp.MustCompile(`(?P<code>[A-Za-z]+):\s*(?P<content>[\s\S]*)(?:<END>)`)
	operationRegexWithoutEnd = regexp.MustCompile(`(?P<code>[A-Za-z]+):\s*(?P<content>[\s\S]*)`)
)

var (
	errNativeFunctionsUnsupported = errors.New("native functions are not supported for this LLM")
)

//go:generate mockgen -source=agent.go -destination=mocks/agent.go -package=mocks
type Agent[T any, S any] interface {
	Run(input T) (S, error)
}

type ChainAgentThought struct {
	Content string
}

func (a *ChainAgentThought) Encode(_ engines.LLM) *engines.ChatMessage {
	return &engines.ChatMessage{
		Role: engines.ConvRoleUser,
		Text: fmt.Sprintf(MessageFormat, ThoughtCode, a.Content),
	}
}

func ParseChainAgentThought(thought *engines.ChatMessage) *ChainAgentThought {
	return &ChainAgentThought{
		Content: strings.TrimPrefix(thought.Text, fmt.Sprintf(MessagePrefix, ThoughtCode)),
	}
}

type ChainAgentAction struct {
	Tool toolsPkg.Tool
	Args json.RawMessage
}

func (a *ChainAgentAction) Encode(targetEngine engines.LLM) *engines.ChatMessage {
	if _, ok := targetEngine.(engines.LLMWithFunctionCalls); ok {
		return &engines.ChatMessage{
			Role: engines.ConvRoleAssistant,
			FunctionCall: &engines.FunctionCall{
				Name: a.Tool.Name(),
				Args: string(a.Args),
			},
		}
	}
	msgText := fmt.Sprintf(MessageFormat, ActionCode, fmt.Sprintf("%s(%s)", a.Tool.Name(), a.Tool.CompactArgs(a.Args)))
	return &engines.ChatMessage{
		Role: engines.ConvRoleAssistant,
		Text: msgText,
	}
}

func (a *ChainAgent[T, S]) parseNativeFunctionCall(msg *engines.ChatMessage) (*ChainAgentAction, error) {
	if msg.FunctionCall == nil {
		return nil, errors.New("no function call found")
	}
	tool, ok := a.Tools[msg.FunctionCall.Name]
	if !ok {
		return nil, fmt.Errorf("tool %q not found. Available tools: %s", msg.FunctionCall.Name, strings.Join(maps.Keys(a.Tools), ", "))
	}
	return &ChainAgentAction{
		Tool: tool,
		Args: []byte(msg.FunctionCall.Args),
	}, nil
}

func (a *ChainAgent[T, S]) ParseChainAgentAction(msg *engines.ChatMessage) (*ChainAgentAction, error) {
	if msg.FunctionCall != nil {
		return a.parseNativeFunctionCall(msg)
	}
	matches := actionRegex.FindStringSubmatch(msg.Text)
	if len(matches) != 3 {
		return nil, fmt.Errorf("invalid action format: message must start with `%s: ` and the action call itself must match regex %q", ActionCode, actionRegex.String())
	}
	toolName := matches[actionRegex.SubexpIndex("tool")]
	toolArgs := matches[actionRegex.SubexpIndex("args")]

	tool, ok := a.Tools[toolName]
	if !ok {
		return nil, fmt.Errorf("tool %q not found. Available tools: %s", toolName, strings.Join(maps.Keys(a.Tools), ", "))
	}

	jsonArgs := json.RawMessage(toolArgs)
	for _, processor := range a.ActionArgPreprocessors {
		var err error
		jsonArgs, err = processor.Process(jsonArgs)
		if err != nil {
			return nil, fmt.Errorf("error while preprocessing action args: %s", err.Error())
		}
	}

	return &ChainAgentAction{
		Tool: tool,
		Args: jsonArgs,
	}, nil
}

type ChainAgentAnswer[S any] struct {
	Content S
}

func (a *ChainAgent[T, S]) parseChainAgentAnswer(answer *engines.ChatMessage) (*ChainAgentAnswer[S], error) {
	output, err := a.Task.AnswerParser(answer.Text)
	if err != nil {
		return nil, err
	}
	return &ChainAgentAnswer[S]{
		Content: output,
	}, nil
}

type ChainAgent[T Representable, S Representable] struct {
	Engine                 engines.LLM
	Task                   *Task[T, S]
	Tools                  map[string]toolsPkg.Tool
	InputValidators        []func(T) error
	OutputValidators       []func(S) error
	MaxSolutionAttempts    int
	MaxRestarts            int
	Memory                 memory.Memory
	ActionConfirmation     func(action *ChainAgentAction) bool
	ActionArgPreprocessors []toolsPkg.PreprocessingTool
	nativeFunctionSpecs    []engines.FunctionSpecs
}

type ChainAgentMessage interface {
	Encode(targetEngine engines.LLM) *engines.ChatMessage
}

type ChainAgentError struct {
	Content  string
	ToolName string
}

func (a *ChainAgentError) Encode(targetEngine engines.LLM) *engines.ChatMessage {
	if _, ok := targetEngine.(engines.LLMWithFunctionCalls); ok {
		return &engines.ChatMessage{
			Role: engines.ConvRoleFunction,
			Name: a.ToolName,
			Text: fmt.Sprintf("An error has occured: %s", a.Content),
		}
	}
	return &engines.ChatMessage{
		Role: engines.ConvRoleSystem,
		Text: fmt.Sprintf(MessageFormat, ErrorCode, a.Content),
	}
}

type ChainAgentObservation struct {
	Content  string
	ToolName string
}

func (a *ChainAgentObservation) Encode(targetEngine engines.LLM) *engines.ChatMessage {
	if _, ok := targetEngine.(engines.LLMWithFunctionCalls); ok {
		return &engines.ChatMessage{
			Role: engines.ConvRoleFunction,
			Name: a.ToolName,
			Text: a.Content,
		}
	}
	return &engines.ChatMessage{
		Role: engines.ConvRoleSystem,
		Text: fmt.Sprintf(MessageFormat, ObservationCode, a.Content),
	}
}

func (a *ChainAgent[T, S]) executeAction(action *ChainAgentAction) (obs ChainAgentMessage) {
	if a.ActionConfirmation != nil && !a.ActionConfirmation(action) {
		return &ChainAgentError{
			Content:  "action cancelled by the user",
			ToolName: action.Tool.Name(),
		}
	}
	actionOutput, err := action.Tool.Execute(action.Args)
	if err != nil {
		return &ChainAgentError{
			Content:  err.Error(),
			ToolName: action.Tool.Name(),
		}
	}
	return &ChainAgentObservation{
		Content:  string(actionOutput),
		ToolName: action.Tool.Name(),
	}
}

func (a *ChainAgent[T, S]) processFunctionCallMessage(response *engines.ChatMessage) (nextMessages []*engines.ChatMessage, answer *ChainAgentAnswer[S]) {
	action, err := a.parseNativeFunctionCall(response)
	if err != nil {
		nextMessages = append(nextMessages, &engines.ChatMessage{
			Role: engines.ConvRoleFunction,
			Name: response.FunctionCall.Name,
			Text: fmt.Sprintf(MessageFormat, ErrorCode, err.Error()),
		})
		return
	}
	nextMessages = append(nextMessages, a.executeAction(action).Encode(a.Engine))
	return
}

func (a *ChainAgent[T, S]) parseResponse(response *engines.ChatMessage) (nextMessages []*engines.ChatMessage, answer *ChainAgentAnswer[S]) {
	if response.FunctionCall != nil {
		return a.processFunctionCallMessage(response)
	}
	var exp *regexp.Regexp
	var ops [][]string
	for _, candidateExp := range []*regexp.Regexp{operationRegex, operationRegexWithoutEnd} {
		ops = candidateExp.FindAllStringSubmatch(response.Text, -1)
		if len(ops) > 0 {
			exp = candidateExp
			break
		}
	}
	if len(ops) == 0 {
		return // consider the message a thought anyway
	}
	for _, op := range ops {
		opCode := op[exp.SubexpIndex("code")]
		opContent := op[exp.SubexpIndex("content")]
		switch opCode {
		case ThoughtCode:
			break
		case ActionCode:
			action, err := a.ParseChainAgentAction(&engines.ChatMessage{
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
			obs := a.executeAction(action)
			nextMessages = append(nextMessages, obs.Encode(a.Engine))
		case AnswerCode:
			answer, err := a.parseChainAgentAnswer(&engines.ChatMessage{
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
			err = a.validateAnswer(answer.Content)
			if err != nil {
				nextMessages = append(nextMessages, &engines.ChatMessage{
					Role: engines.ConvRoleSystem,
					Text: fmt.Sprintf(MessageFormat, ErrorCode, err.Error()),
				})
				break
			}
			return nextMessages, answer
		default:
			break // consider the message a thought
		}
	}
	return nextMessages, nil
}

func (a *ChainAgent[T, S]) validateAnswer(answer S) error {
	var answerErr *multierror.Error
	for _, validator := range a.OutputValidators {
		answerErr = multierror.Append(answerErr, validator(answer))
	}
	return answerErr.ErrorOrNil()
}

func (a *ChainAgent[T, S]) logMessages(msg ...*engines.ChatMessage) {
	for _, m := range msg {
		if m.FunctionCall != nil {
			log.Debugf("[%s] [function_call] %s(%s)", m.Role, m.FunctionCall.Name, m.FunctionCall.Args)
			continue
		}
		log.Debugf("[%s] %s", m.Role, m.Text)
	}
}

func (a *ChainAgent[T, S]) chat(prompt *engines.ChatPrompt) (*engines.ChatMessage, error) {
	if engine, ok := a.Engine.(engines.LLMWithFunctionCalls); ok {
		return engine.ChatWithFunctions(prompt, a.nativeFunctionSpecs)
	}
	return a.Engine.Chat(prompt)
}

func (a *ChainAgent[T, S]) run(input T) (output S, err error) {
	var inputErr *multierror.Error
	for _, validator := range a.InputValidators {
		inputErr = multierror.Append(inputErr, validator(input))
	}
	if inputErr.ErrorOrNil() != nil {
		return output, fmt.Errorf("invalid input: %w", inputErr)
	}
	visibleTools := a.Tools
	if _, ok := a.Engine.(engines.LLMWithFunctionCalls); ok {
		visibleTools = map[string]toolsPkg.Tool{}
	}
	taskPrompt := a.Task.Compile(input, visibleTools)
	a.logMessages(taskPrompt.History...)
	err = a.Memory.AddPrompt(taskPrompt)
	if err != nil {
		return output, fmt.Errorf("failed to add prompt to memory: %w", err)
	}
	response, err := a.chat(taskPrompt)
	if err != nil {
		return output, fmt.Errorf("failed to predict response: %w", err)
	}
	a.logMessages(response)
	err = a.Memory.Add(response)
	if err != nil {
		return output, fmt.Errorf("failed to add response to memory: %w", err)
	}
	stepsExecuted := 0
	for {
		nextMessages, answer := a.parseResponse(response)
		a.logMessages(nextMessages...)
		if answer != nil {
			return answer.Content, nil
		}
		prompt, err := a.Memory.PromptWithContext(nextMessages...)
		if err != nil {
			return output, fmt.Errorf("failed to generate prompt: %w", err)
		}
		if a.MaxSolutionAttempts > 0 && stepsExecuted > a.MaxSolutionAttempts {
			return output, errors.New("max solution attempts reached")
		}
		response, err = a.chat(prompt)
		if err != nil {
			return output, fmt.Errorf("failed to predict response: %w", err)
		}
		a.logMessages(response)
		err = a.Memory.Add(response)
		if err != nil {
			return output, fmt.Errorf("failed to add response to memory: %w", err)
		}
		stepsExecuted++
	}
}

func (a *ChainAgent[T, S]) Run(input T) (output S, err error) {
	for i := 0; i <= a.MaxRestarts; i++ {
		output, err = a.run(input)
		if err == nil {
			return output, nil
		}
	}
	return output, err
}

func NewChainAgent[T Representable, S Representable](engine engines.LLM, task *Task[T, S], memory memory.Memory) *ChainAgent[T, S] {
	return &ChainAgent[T, S]{
		Engine: engine,
		Task:   task,
		Tools:  map[string]toolsPkg.Tool{},
		Memory: memory,
		ActionArgPreprocessors: []toolsPkg.PreprocessingTool{
			toolsPkg.NewJSONAutoFixer(engine, 3),
		},
	}
}

func (a *ChainAgent[T, S]) setNativeLLMFunctions(tools ...toolsPkg.Tool) (err error) {
	_, ok := a.Engine.(engines.LLMWithFunctionCalls)
	if !ok {
		return errNativeFunctionsUnsupported
	}
	functions := make([]engines.FunctionSpecs, len(tools))
	for i, tool := range tools {
		function, err := toolsPkg.ConvertToNativeFunctionSpecs(tool)
		if err != nil {
			return fmt.Errorf("failed to convert tool to native function specs: %w", err)
		}
		functions[i] = function
	}
	a.nativeFunctionSpecs = functions
	return nil
}

func (a *ChainAgent[T, S]) WithTools(tools ...toolsPkg.Tool) *ChainAgent[T, S] {
	err := a.setNativeLLMFunctions(tools...)
	if err != nil && !errors.Is(err, errNativeFunctionsUnsupported) {
		log.Warnf("failed to set native LLM functions, using fallback: %v", err)
	}
	for _, tool := range tools {
		a.Tools[tool.Name()] = tool
		if preprocessor, ok := tool.(toolsPkg.PreprocessingTool); ok {
			a.ActionArgPreprocessors = append(a.ActionArgPreprocessors, preprocessor)
		}
	}
	return a
}

func (a *ChainAgent[T, S]) WithInputValidators(validators ...func(T) error) *ChainAgent[T, S] {
	a.InputValidators = append(a.InputValidators, validators...)
	return a
}

func (a *ChainAgent[T, S]) WithOutputValidators(validators ...func(S) error) *ChainAgent[T, S] {
	a.OutputValidators = append(a.OutputValidators, validators...)
	return a
}

func (a *ChainAgent[T, S]) WithMaxSolutionAttempts(max int) *ChainAgent[T, S] {
	a.MaxSolutionAttempts = max
	return a
}

func (a *ChainAgent[T, S]) WithActionConfirmation(actionConfirmationProvider func(*ChainAgentAction) bool) *ChainAgent[T, S] {
	a.ActionConfirmation = actionConfirmationProvider
	return a
}

func (a *ChainAgent[T, S]) WithRestarts(maxRestarts int) *ChainAgent[T, S] {
	a.MaxRestarts = maxRestarts
	return a
}
