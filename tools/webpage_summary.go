package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/natexcvi/go-llm/engines"
	"golang.org/x/net/html"
)

type WebpageSummary struct {
	model engines.LLM
}

func (*WebpageSummary) stripHTMLTags(s string) string {
	// Parse the HTML string into a token stream
	tokenizer := html.NewTokenizer(bytes.NewBufferString(s))

	// Use a buffer to accumulate the plain text
	buffer := new(bytes.Buffer)

	// Iterate over the token stream
	for {
		// Get the next token
		tokenType := tokenizer.Next()

		// Stop if we've reached the end of the stream
		if tokenType == html.ErrorToken {
			return buffer.String()
		}

		// Get the current token
		token := tokenizer.Token()

		// If the token is not a start or end tag, write its content to the buffer
		if tokenType == html.TextToken {
			buffer.WriteString(token.Data)
		}
	}
}

func (w *WebpageSummary) getStrippedWebpage(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to get webpage: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to get webpage: %s", resp.Status)
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}
	return w.stripHTMLTags(string(body)), nil
}

func (w *WebpageSummary) summariseContent(url, focusOn string) (string, error) {
	content, err := w.getStrippedWebpage(url)
	if err != nil {
		return "", fmt.Errorf("failed to get stripped webpage: %w", err)
	}
	focusInstruction := ""
	if focusOn != "" {
		focusInstruction = fmt.Sprintf(", focusing on %s", focusOn)
	}
	prompt := engines.ChatPrompt{
		History: []*engines.ChatMessage{
			{
				Role: engines.ConvRoleSystem,
				Text: "You are a helpful assistant that summarises " +
					"contents of web pages you are given. ",
			},
			{
				Role: engines.ConvRoleUser,
				Text: fmt.Sprintf("Summarise the following web page please%s:\n%s", focusInstruction, content),
			},
		},
	}
	summary, err := w.model.Predict(&prompt)
	if err != nil {
		return "", fmt.Errorf("failed to predict: %w", err)
	}
	return summary.Text, nil
}

func (w *WebpageSummary) Execute(args json.RawMessage) (json.RawMessage, error) {
	var command struct {
		URL     string `json:"url"`
		FocusOn string `json:"focus_on"`
	}
	err := json.Unmarshal(args, &command)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal args: %w", err)
	}
	summary, err := w.summariseContent(command.URL, command.FocusOn)
	if err != nil {
		return nil, fmt.Errorf("failed to summarise content: %w", err)
	}
	return json.Marshal(summary)
}

func (w *WebpageSummary) Name() string {
	return "webpage_summary"
}

func (w *WebpageSummary) Description() string {
	return "Summarises the content of a web page, given its URL and an optional focus instruction."
}

func (w *WebpageSummary) ArgsSchema() json.RawMessage {
	return []byte(`{
		"url": "the URL of the web page to summarise",
		"focus_on": "an optional instruction to focus on a specific part of the web page."
	}`)
}
