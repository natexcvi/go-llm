package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/playwright-community/playwright-go"
)

type SearchResult struct {
	Title string
	URL   string
}

type WebSearch struct {
	ServiceURL string
}

func NewWebSearch(serviceURL string) *WebSearch {
	return &WebSearch{
		ServiceURL: serviceURL,
	}
}

func NewGoogleSearch() *WebSearch {
	return &WebSearch{
		ServiceURL: "https://google.com/",
	}
}

func (ws *WebSearch) Execute(args json.RawMessage) (json.RawMessage, error) {
	var query struct {
		Query string `json:"query"`
	}
	err := json.Unmarshal(args, &query)
	if err != nil {
		return nil, err
	}
	results, err := ws.search(query.Query)
	if err != nil {
		return nil, err
	}
	return json.Marshal(results)
}

func (ws *WebSearch) Name() string {
	return "google_search"
}

func (ws *WebSearch) Description() string {
	return fmt.Sprintf("A tool for searching the web using Google.")
}

func (ws *WebSearch) ArgsSchema() json.RawMessage {
	return json.RawMessage(`{"query": "the search query"}`)
}

func (ws *WebSearch) search(query string) (searchResults []SearchResult, err error) {
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = playwright.Install()
	if err != nil {
		return nil, fmt.Errorf("could not install playwright: %v", err)
	}
	pw, err := playwright.Run()
	if err != nil {
		return nil, fmt.Errorf("could not start playwright: %v", err)
	}
	defer pw.Stop()

	// Launch a new Chromium browser context
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		SlowMo: playwright.Float(100),
	})
	if err != nil {
		return nil, fmt.Errorf("could not launch browser: %v", err)
	}
	defer browser.Close()

	// Create a new browser page
	page, err := browser.NewPage()
	if err != nil {
		return nil, fmt.Errorf("could not create page: %v", err)
	}
	defer page.Close()

	// Navigate to Google and search for query
	if _, err := page.Goto(ws.ServiceURL); err != nil {
		return nil, fmt.Errorf("could not navigate to google: %v", err)
	}
	if err := page.Fill("input[name=\"q\"]", query); err != nil {
		return nil, fmt.Errorf("could not fill search input: %v", err)
	}
	if err := page.Keyboard().Press("Enter"); err != nil {
		return nil, fmt.Errorf("could not press enter: %v", err)
	}

	// Wait for the search results to load
	if _, err := page.WaitForSelector("#search"); err != nil {
		return nil, fmt.Errorf("could not find search results: %v", err)
	}

	// scroll to the bottom of the page
	if _, err := page.Evaluate(`() => {
		window.scrollBy(0, window.innerHeight);
	}`); err != nil {
		return nil, fmt.Errorf("could not scroll to bottom of page: %v", err)
	}

	// wait for last result to load
	if _, err := page.WaitForSelector("#search .g:last-child"); err != nil {
		return nil, fmt.Errorf("could not find last search result: %v", err)
	}

	// wait for page to load completely
	page.WaitForLoadState(string(*playwright.LoadStateDomcontentloaded))

	// Parse the search results
	results, err := page.QuerySelectorAll("#search .g")
	if err != nil {
		return nil, fmt.Errorf("could not query search results: %v", err)
	}
	for _, result := range results {
		title, err := result.QuerySelector(".LC20lb.DKV0Md")
		if err != nil {
			continue
		}
		titleText, err := title.InnerText()
		if err != nil {
			continue
		}

		urlElem, err := result.QuerySelector("a")
		if err != nil {
			continue
		}
		url, err := urlElem.GetAttribute("href")
		if err != nil {
			continue
		}
		searchResults = append(searchResults, SearchResult{
			Title: titleText,
			URL:   url,
		})
	}
	return searchResults, nil
}

func (ws *WebSearch) CompactArgs(args json.RawMessage) json.RawMessage {
	return args
}
