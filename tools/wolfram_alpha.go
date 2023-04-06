package tools

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
)

type WolframAlpha struct {
	ServiceURL string
	AppID      string
}

func NewWolframAlpha(appID string) *WolframAlpha {
	return &WolframAlpha{
		ServiceURL: "http://api.wolframalpha.com/v1/result",
		AppID:      appID,
	}
}

func NewWolframAlphaWithServiceURL(serviceURL, appID string) *WolframAlpha {
	return &WolframAlpha{
		ServiceURL: serviceURL,
		AppID:      appID,
	}
}

func (wa *WolframAlpha) shortAnswer(query string) (answer string, err error) {
	req, err := http.NewRequest("GET", wa.ServiceURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	q := req.URL.Query()
	q.Add("appid", wa.AppID)
	q.Add("i", query)
	req.URL.RawQuery = q.Encode()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to get short answer: %s", resp.Status)
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}
	return string(body), nil
}

func (wa *WolframAlpha) Execute(args json.RawMessage) (json.RawMessage, error) {
	var query struct {
		Query string `json:"query"`
	}
	err := json.Unmarshal(args, &query)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal args: %w", err)
	}
	answer, err := wa.shortAnswer(query.Query)
	if err != nil {
		return nil, fmt.Errorf("failed to query WolframAlpha: %w", err)
	}
	return json.Marshal(answer)
}

func (wa *WolframAlpha) Name() string {
	return "wolfram_alpha"
}

func (wa *WolframAlpha) Description() string {
	return "A tool for querying WolframAlpha. Use it for " +
		"factual information retrieval, calculations, etc."
}

func (wa *WolframAlpha) ArgsSchema() json.RawMessage {
	return json.RawMessage(`{"query": "the search query, e.g. '2+2' or 'what is the capital of France?'"}`)
}
