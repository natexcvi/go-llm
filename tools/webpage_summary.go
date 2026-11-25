package tools

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
	"github.com/natexcvi/go-llm/engines"
)

// WebpageSummary budgets are measured in bytes of UTF-8 encoded role and message text. Bytes are used instead of
// provider-specific tokens so the limit is independent of the model implementation. The context budget covers both
// the request and a reserve for the response.
const (
	defaultContextBudgetBytes   = 12000
	defaultResponseReserveBytes = 2000
	summarySoFarIntro           = ".\nHere is the summary so far, from the earlier parts: "
)

type WebpageSummary struct {
	model                engines.LLM
	contextBudgetBytes   int
	responseReserveBytes int
}

// promptBytes measures the units defined above: the UTF-8 bytes in every role and message content in the prompt.
func promptBytes(prompt *engines.ChatPrompt) int {
	total := 0
	for _, message := range prompt.History {
		total += len(string(message.Role)) + len(message.Text)
	}
	return total
}

// utf8PrefixLength returns the longest prefix of text that is at most maxBytes long and ends on a UTF-8 boundary.
func utf8PrefixLength(text string, maxBytes int) int {
	if maxBytes < 0 {
		return 0
	}
	if len(text) <= maxBytes {
		return len(text)
	}
	end := 0
	for end < len(text) {
		_, width := utf8.DecodeRuneInString(text[end:])
		if end+width > maxBytes {
			break
		}
		end += width
	}
	return end
}

func truncateUTF8(text string, maxBytes int) string {
	return text[:utf8PrefixLength(text, maxBytes)]
}

// splitIntoChunks splits text at whitespace into pieces of at most size bytes. A whitespace-free span longer than
// size is split on UTF-8 boundaries so no content is lost and no chunk exceeds the byte limit.
func splitIntoChunks(text string, size int) []string {
	if size <= 0 {
		size = defaultContextBudgetBytes
	}
	if size < utf8.UTFMax {
		size = utf8.UTFMax
	}
	words := strings.Fields(text)
	var chunks []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			chunks = append(chunks, current.String())
			current.Reset()
		}
	}
	for _, word := range words {
		for len(word) > 0 {
			if current.Len() == 0 {
				if len(word) <= size {
					current.WriteString(word)
					break
				}
				partLength := utf8PrefixLength(word, size)
				chunks = append(chunks, word[:partLength])
				word = word[partLength:]
				continue
			}
			if current.Len()+1+len(word) <= size {
				current.WriteByte(' ')
				current.WriteString(word)
				break
			}
			flush()
		}
	}
	flush()
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

func makeWebpageSummaryPrompt(instruction, chunk string) *engines.ChatPrompt {
	return &engines.ChatPrompt{
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
}

// fixedWebpagePromptBytes conservatively measures the fixed scaffolding for the largest possible part number and
// for a prompt that carries a previous summary. Variable summary and chunk bytes are added to this base.
func fixedWebpagePromptBytes(focusInstruction string, maxParts int) int {
	instruction := fmt.Sprintf("Summarise part %d of %d please%s", maxParts, maxParts, focusInstruction)
	instruction += summarySoFarIntro
	return promptBytes(makeWebpageSummaryPrompt(instruction, ""))
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
	inputBudgetBytes := w.contextBudgetBytes - w.responseReserveBytes
	if inputBudgetBytes <= 0 {
		return "", fmt.Errorf(
			"webpage summary fixed instructions cannot fit: the %d byte context budget leaves no request bytes after reserving %d bytes for the response",
			w.contextBudgetBytes,
			w.responseReserveBytes,
		)
	}
	maxParts := len(content) + 1
	fixedBytes := fixedWebpagePromptBytes(focusInstruction, maxParts)
	if fixedBytes >= inputBudgetBytes {
		return "", fmt.Errorf(
			"webpage summary fixed instructions cannot fit: they use %d bytes of the %d byte request budget after reserving %d response bytes",
			fixedBytes,
			inputBudgetBytes,
			w.responseReserveBytes,
		)
	}
	remainingBytes := inputBudgetBytes - fixedBytes
	summaryCarryBudgetBytes := remainingBytes / 2
	chunkBudgetBytes := remainingBytes - summaryCarryBudgetBytes
	if chunkBudgetBytes < utf8.UTFMax {
		chunkBudgetBytes = remainingBytes
		if chunkBudgetBytes > utf8.UTFMax {
			chunkBudgetBytes = utf8.UTFMax
		}
		summaryCarryBudgetBytes = remainingBytes - chunkBudgetBytes
	}
	if chunkBudgetBytes < utf8.UTFMax {
		return "", fmt.Errorf(
			"webpage summary fixed instructions cannot fit: only %d request bytes remain, but %d bytes are required for one UTF-8 encoded character",
			remainingBytes,
			utf8.UTFMax,
		)
	}

	chunks := splitIntoChunks(content, chunkBudgetBytes)
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
		carriedSummary := truncateUTF8(cumulative, summaryCarryBudgetBytes)
		if carriedSummary != "" {
			instruction += summarySoFarIntro + carriedSummary
		}
		prompt := makeWebpageSummaryPrompt(instruction, chunk)
		if promptBytes(prompt) > inputBudgetBytes {
			return "", fmt.Errorf(
				"webpage summary request exceeded its budget: measured %d bytes against a %d byte request budget",
				promptBytes(prompt),
				inputBudgetBytes,
			)
		}
		summary, err := w.model.Chat(prompt)
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

// NewWebpageSummaryWithBudget sets the total byte budget for the model context. The budget is measured in UTF-8
// bytes of role and message text, and responseReserveBytes is the part of that budget kept free for the reply.
func NewWebpageSummaryWithBudget(model engines.LLM, contextBudgetBytes, responseReserveBytes int) *WebpageSummary {
	if contextBudgetBytes <= 0 {
		contextBudgetBytes = defaultContextBudgetBytes
	}
	if responseReserveBytes <= 0 {
		responseReserveBytes = defaultResponseReserveBytes
	}
	return &WebpageSummary{
		model:                model,
		contextBudgetBytes:   contextBudgetBytes,
		responseReserveBytes: responseReserveBytes,
	}
}

func NewWebpageSummary(model engines.LLM) *WebpageSummary {
	return NewWebpageSummaryWithBudget(model, defaultContextBudgetBytes, defaultResponseReserveBytes)
}
