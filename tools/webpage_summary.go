package tools

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/natexcvi/go-llm/engines"
)

type WebpageSummary struct {
	model engines.LLM
}

func (*WebpageSummary) stripHTMLTags(s string) string {
	// Remove HTML tags
	document, err := goquery.NewDocumentFromReader(strings.NewReader(s))
	if err != nil {
		return ""
	}
	document.Find("script, style").Each(func(index int, item *goquery.Selection) {
		item.Remove()
	})
	text := document.Text()

	// Remove JavaScript code
	re := regexp.MustCompile(`(?m)^<script.*$[\r\n]*`)
	text = re.ReplaceAllString(text, "")

	// Remove extra whitespace
	re = regexp.MustCompile(`\s+`)
	text = re.ReplaceAllString(text, " ")
	return text
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
	summary, err := w.predict(&prompt)
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

func (w *WebpageSummary) CompactArgs(args json.RawMessage) json.RawMessage {
	return args
}

func (w *WebpageSummary) predict(prompt *engines.ChatPrompt) (*engines.ChatMessage, error) {
	if model, ok := w.model.(engines.LLMWithFunctionCalls); ok {
		return model.PredictWithoutFunctions(prompt)
	}
	return w.model.Predict(prompt)
}

func NewWebpageSummary(model engines.LLM) *WebpageSummary {
	return &WebpageSummary{model: model}
}
