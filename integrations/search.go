package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/capabilities"
)

type SearchAPI struct {
	Client          *http.Client
	BaseURL, APIKey string
}

func (api SearchAPI) post(ctx context.Context, base, path string, body, out any) error {
	if api.APIKey == "" {
		return errors.New("search API key required")
	}
	if api.BaseURL != "" {
		base = strings.TrimRight(api.BaseURL, "/")
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", api.APIKey)
	c := api.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s: %s", path, resp.Status)
	}
	b, err = io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil {
		return err
	}
	if len(b) > 8<<20 {
		return errors.New("search API response exceeds 8 MiB")
	}
	return json.Unmarshal(b, out)
}

type Exa SearchAPI
type exaResults struct {
	Results []struct {
		Title, URL, Text string
		Highlights       []string
	}
}

func (e Exa) Search(ctx context.Context, q string) ([]capabilities.Source, error) {
	var resp exaResults
	err := SearchAPI(e).post(ctx, "https://api.exa.ai", "/search", map[string]any{"query": q, "numResults": 8, "contents": map[string]bool{"highlights": true}}, &resp)
	if err != nil {
		return nil, err
	}
	if resp.Results == nil {
		return nil, errors.New("missing Exa results")
	}
	sources := []capabilities.Source{}
	for _, p := range resp.Results {
		sources = append(sources, capabilities.Source{URL: p.URL, Title: p.Title, Text: strings.Join(p.Highlights, "\n")})
	}
	return sources, nil
}
func (e Exa) Fetch(ctx context.Context, address string) (capabilities.Source, error) {
	var resp exaResults
	err := SearchAPI(e).post(ctx, "https://api.exa.ai", "/contents", map[string]any{"urls": []string{address}, "text": true}, &resp)
	if err != nil {
		return capabilities.Source{}, err
	}
	if len(resp.Results) == 0 {
		return capabilities.Source{}, errors.New("no contents for URL")
	}
	p := resp.Results[0]
	return capabilities.Source{URL: p.URL, Title: p.Title, Text: p.Text}, nil
}

type You SearchAPI

func (y You) Search(ctx context.Context, q string) ([]capabilities.Source, error) {
	var resp struct {
		Results *struct {
			Web []struct {
				URL, Title, Description string
				Snippets                []string
			}
		}
	}
	err := SearchAPI(y).post(ctx, "https://api.you.com", "/v1/search", map[string]any{"query": q, "count": 8}, &resp)
	if err != nil {
		return nil, err
	}
	if resp.Results == nil {
		return nil, errors.New("missing You.com results")
	}
	sources := []capabilities.Source{}
	for _, p := range resp.Results.Web {
		text := strings.Join(p.Snippets, "\n")
		if text == "" {
			text = p.Description
		}
		sources = append(sources, capabilities.Source{Title: p.Title, URL: p.URL, Text: text})
	}
	return sources, nil
}
func (y You) Fetch(ctx context.Context, address string) (capabilities.Source, error) {
	var pages []struct{ Title, URL, Markdown string }
	err := SearchAPI(y).post(ctx, "https://api.you.com", "/v1/contents", map[string]any{"urls": []string{address}, "formats": []string{"markdown"}}, &pages)
	if err != nil {
		return capabilities.Source{}, err
	}
	if len(pages) == 0 {
		return capabilities.Source{}, errors.New("empty You.com contents")
	}
	p := pages[0]
	return capabilities.Source{Title: p.Title, URL: p.URL, Text: p.Markdown}, nil
}
func (y You) Research() h.Capability {
	return h.Tools(h.NewTool("you_research", "Research a question with You.com and return its answer and sources.", func(ctx context.Context, in struct {
		Input string `json:"input"`
	}) (json.RawMessage, error) {
		var resp struct{ Output json.RawMessage }
		err := SearchAPI(y).post(ctx, "https://api.you.com", "/v1/research", map[string]string{"input": in.Input}, &resp)
		if err == nil && len(resp.Output) == 0 {
			err = errors.New("missing You.com research output")
		}
		return resp.Output, err
	}))
}
