package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// --- Brave Search Provider ---

type braveSearchProvider struct {
	apiKey     string
	maxResults int
	client     *http.Client
}

func newBraveSearchProvider(apiKey string, maxResults int) *braveSearchProvider {
	return &braveSearchProvider{
		apiKey:     apiKey,
		maxResults: normalizeProviderMaxResults(maxResults),
		client:     &http.Client{Timeout: time.Duration(searchTimeoutSeconds) * time.Second},
	}
}

func (p *braveSearchProvider) Name() string { return searchProviderBrave }

func (p *braveSearchProvider) Search(ctx context.Context, params searchParams) ([]searchResult, error) {
	count := clampProviderResultCount(params.Count, p.maxResults)

	q := url.Values{}
	q.Set("q", params.Query)
	q.Set("count", fmt.Sprintf("%d", count))

	if country := normalizeBraveCountry(params.Country); country != "" {
		q.Set("country", country)
	}
	if params.SearchLang != "" {
		q.Set("search_lang", strings.ToLower(strings.TrimSpace(params.SearchLang)))
	}
	if uiLang := normalizeBraveUILang(params.UILang); uiLang != "" {
		q.Set("ui_lang", uiLang)
	}
	if f := normalizeFreshness(params.Freshness); f != "" {
		q.Set("freshness", f)
	}

	reqURL := braveSearchEndpoint + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("brave API returned %d: %s", resp.StatusCode, truncateStr(string(body), 200))
	}

	var braveResp struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}

	if err := json.Unmarshal(body, &braveResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	results := make([]searchResult, 0, len(braveResp.Web.Results))
	for _, r := range braveResp.Web.Results {
		results = append(results, searchResult{
			Title:       r.Title,
			URL:         r.URL,
			Description: r.Description,
		})
	}
	return results, nil
}

func normalizeBraveCountry(country string) string {
	c := strings.ToUpper(strings.TrimSpace(country))
	if c == "" || c == "ALL" {
		return ""
	}

	// Brave's web-search country enum does not currently accept Vietnam (VN).
	// Omitting this parameter still works for Vietnamese queries and avoids a
	// hard 422 that prevents Brave from being usable as the first provider.
	if c == "VN" {
		return ""
	}

	if len(c) != 2 {
		return ""
	}
	return c
}

func normalizeBraveUILang(uiLang string) string {
	lang := strings.TrimSpace(uiLang)
	if lang == "" {
		return ""
	}

	// Brave expects a locale-style value such as en-US. Models often pass bare
	// language codes like "vi" or "en", which Brave rejects with 422.
	if len(lang) != 5 || lang[2] != '-' {
		return ""
	}
	return strings.ToLower(lang[:2]) + "-" + strings.ToUpper(lang[3:])
}
