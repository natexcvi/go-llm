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

// defaultChunkSize keeps each LLM prompt small enough for pages of arbitrary length.
const defaultChunkSize = 12000

type WebpageSummary struct {
	model     engines.LLM
	chunkSize int
}

// splitIntoChunks splits text at whitespace into pieces of at most size characters. A word longer than size is
// kept whole so no content is lost.
func splitIntoChunks(text string, size int) []string {
	if size <= 0 {
		size = defaultChunkSize
	}
	words := strings.Fields(text)
	var chunks []string
	var current strings.Builder
	for _, word := range words {
		if current.Len() == 0 {
			current.WriteString(word)
			continue
		}
		if current.Len()+1+len(word) > size {
			chunks = append(chunks, current.String())
			current.Reset()
			current.WriteString(word)
			continue
		}
		current.WriteByte(' ')
		current.WriteString(word)
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
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
	chunks := splitIntoChunks(content, w.chunkSize)
	if len(chunks) == 0 {
		chunks = []string{""}
	}
	cumulative := ""
	for index, chunk := range chunks {
		scope := fmt.Sprintf("part %d of %d", index+1, len(chunks))
		if len(chunks) == 1 {
			scope = "the web page"
		}
		instruction := fmt.Sprintf("Summarise %s please%s", scope, focusInstruction)
		if cumulative != "" {
			instruction += fmt.Sprintf(".\nHere is the summary so far, from the earlier parts: %s", cumulative)
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
					Text: fmt.Sprintf("%s:\n%s", instruction, chunk),
				},
			},
		}
		summary, err := w.model.Chat(&prompt)
		if err != nil {
			return "", fmt.Errorf("failed to predict: %w", err)
		}
		cumulative = summary.Text
	}
	return cumulative, nil
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

// NewWebpageSummaryWithChunkSize sets the maximum characters sent to the LLM per part. It is useful for tests and
// for models with a smaller context window.
func NewWebpageSummaryWithChunkSize(model engines.LLM, chunkSize int) *WebpageSummary {
	if chunkSize <= 0 {
		chunkSize = defaultChunkSize
	}
	return &WebpageSummary{model: model, chunkSize: chunkSize}
}

func NewWebpageSummary(model engines.LLM) *WebpageSummary {
	return NewWebpageSummaryWithChunkSize(model, defaultChunkSize)
}
