package engines

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

var ErrTokenLimitExceeded = fmt.Errorf("token limit exceeded")

type GPT struct {
	APIToken             string
	Model                string
	PromptTokensUsed     int
	CompletionTokensUsed int
	PromptTokenLimit     int
	CompletionTokenLimit int
	TotalTokenLimit      int
	functions            []FunctionSpecs
	Temperature          float64
}

type ChatCompletionRequest struct {
	Model       string          `json:"model"`
	Temperature float64         `json:"temperature,omitempty"`
	Messages    []*ChatMessage  `json:"messages"`
	Functions   []FunctionSpecs `json:"functions,omitempty"`
}

type ChatCompletionResponse struct {
	Choices []struct {
		Message *ChatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokensUsed     int `json:"prompt_tokens"`
		CompletionTokensUsed int `json:"completion_tokens"`
	} `json:"usage"`
}

func (gpt *GPT) chat(prompt *ChatPrompt, useFunctions bool) (*ChatMessage, error) {
	if gpt.isLimitExceeded() {
		return nil, ErrTokenLimitExceeded
	}
	completionRequest := ChatCompletionRequest{
		Model:       gpt.Model,
		Messages:    prompt.History,
		Temperature: gpt.Temperature,
	}
	if useFunctions {
		completionRequest.Functions = gpt.functions
	}
	bodyJSON, err := json.Marshal(completionRequest)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(
		"POST",
		"https://api.openai.com/v1/chat/completions",
		bytes.NewBuffer([]byte(bodyJSON)),
	)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Authorization", "Bearer "+gpt.APIToken)
	req.Header.Add("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	return gpt.parseResponseBody(res.Body)
}

func (gpt *GPT) SetFunctions(funcs ...FunctionSpecs) {
	gpt.functions = funcs
}

func (gpt *GPT) ChatWithFunctions(prompt *ChatPrompt) (*ChatMessage, error) {
	return gpt.chat(prompt, true)
}

func (gpt *GPT) Chat(prompt *ChatPrompt) (*ChatMessage, error) {
	return gpt.chat(prompt, false)
}

func (gpt *GPT) isLimitExceeded() bool {
	return gpt.PromptTokenLimit > 0 && gpt.PromptTokensUsed > gpt.PromptTokenLimit ||
		gpt.CompletionTokenLimit > 0 && gpt.CompletionTokensUsed > gpt.CompletionTokenLimit ||
		gpt.TotalTokenLimit > 0 && gpt.PromptTokensUsed+gpt.CompletionTokensUsed > gpt.TotalTokenLimit
}

func (gpt *GPT) parseResponseBody(body io.Reader) (*ChatMessage, error) {
	var buf bytes.Buffer
	tee := io.TeeReader(body, &buf)
	var response ChatCompletionResponse
	err := json.NewDecoder(tee).Decode(&response)
	if err != nil {
		return nil, err
	}
	gpt.PromptTokensUsed += response.Usage.PromptTokensUsed
	gpt.CompletionTokensUsed += response.Usage.CompletionTokensUsed
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response: %s", buf.String())
	}
	if response.Choices[0].Message.FunctionCall == nil && response.Choices[0].Message.Text == "" {
		return nil, fmt.Errorf("no content in response: %s", buf.String())
	}
	return response.Choices[0].Message, nil
}

func NewGPTEngine(apiToken string, model string) *GPT {
	return &GPT{
		APIToken:    apiToken,
		Model:       model,
		Temperature: 1,
	}
}

func (gpt *GPT) WithPromptTokenLimit(limit int) *GPT {
	gpt.PromptTokenLimit = limit
	return gpt
}

func (gpt *GPT) WithCompletionTokenLimit(limit int) *GPT {
	gpt.CompletionTokenLimit = limit
	return gpt
}

func (gpt *GPT) WithTotalTokenLimit(limit int) *GPT {
	gpt.TotalTokenLimit = limit
	return gpt
}

func (gpt *GPT) WithTemperature(temperature float64) *GPT {
	gpt.Temperature = temperature
	return gpt
}
