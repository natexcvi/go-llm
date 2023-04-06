package engines

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type GPT struct {
	APIToken string
	Model    string
}

type ChatCompletionRequest struct {
	Model    string         `json:"model"`
	Messages []*ChatMessage `json:"messages"`
}

type ChatCompletionResponse struct {
	Choices []struct {
		Message *ChatMessage `json:"message"`
	} `json:"choices"`
}

func (gpt *GPT) Predict(prompt *ChatPrompt) (*ChatMessage, error) {
	bodyJSON, err := json.Marshal(ChatCompletionRequest{
		Model:    gpt.Model,
		Messages: prompt.History,
	})
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

func (gpt *GPT) parseResponseBody(body io.Reader) (*ChatMessage, error) {
	var buf bytes.Buffer
	tee := io.TeeReader(body, &buf)
	var response ChatCompletionResponse
	err := json.NewDecoder(tee).Decode(&response)
	if err != nil {
		return nil, err
	}
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response: %s", buf.String())
	}
	return response.Choices[0].Message, nil
}

func NewGPTEngine(apiToken string, model string) LLM {
	return &GPT{
		APIToken: apiToken,
		Model:    model,
	}
}
