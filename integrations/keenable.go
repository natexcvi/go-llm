package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/natexcvi/go-llm/capabilities"
)

// Keenable implements the public and API-key search and page fetching APIs.
type Keenable struct {
	Client          *http.Client
	BaseURL, APIKey string
}
type keenPage struct{ Title, URL, Content, Snippet string }

func (k Keenable) request(ctx context.Context, method, resource string, query url.Values, body any, out any) error {
	base := k.BaseURL
	if base == "" {
		base = "https://api.keenable.ai"
	}
	endpoint := strings.TrimRight(base, "/") + "/v1/" + resource
	if k.APIKey == "" {
		endpoint += "/public"
	}
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	if k.APIKey != "" {
		req.Header.Set("X-API-Key", k.APIKey)
	}
	req.Header.Set("X-Keenable-Title", "go-llm")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := k.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("keenable %s: %s", resource, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil {
		return err
	}
	if len(b) > 8<<20 {
		return errors.New("keenable response exceeds 8 MiB")
	}
	return json.Unmarshal(b, out)
}
func (k Keenable) Search(ctx context.Context, q string) ([]capabilities.Source, error) {
	var response struct{ Results []keenPage }
	err := k.request(ctx, "POST", "search", nil, map[string]any{"query": q, "max_results": 8}, &response)
	if err != nil {
		return nil, err
	}
	if response.Results == nil {
		return nil, errors.New("keenable response missing results")
	}
	items := []capabilities.Source{}
	for _, v := range response.Results {
		if v.URL == "" {
			continue
		}
		text := v.Content
		if text == "" {
			text = v.Snippet
		}
		items = append(items, capabilities.Source{Title: v.Title, URL: v.URL, Text: text})
	}
	return items, nil
}
func (k Keenable) Fetch(ctx context.Context, address string) (capabilities.Source, error) {
	var p keenPage
	u, err := url.ParseRequestURI(address)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return capabilities.Source{}, errors.New("an absolute HTTP(S) URL is required")
	}
	err = k.request(ctx, "GET", "fetch", url.Values{"url": {address}}, nil, &p)
	if p.URL == "" {
		p.URL = address
	}
	if err == nil && p.Content == "" {
		err = errors.New("page has no readable content")
	}
	return capabilities.Source{URL: p.URL, Title: p.Title, Text: p.Content}, err
}
