package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

type GoogleMapsPlaceTool struct {
	secrets store.ConfigSecretsStore
	client  *http.Client
}

func NewGoogleMapsPlaceTool(secrets store.ConfigSecretsStore) *GoogleMapsPlaceTool {
	return &GoogleMapsPlaceTool{secrets: secrets, client: &http.Client{Timeout: 20 * time.Second}}
}

func (t *GoogleMapsPlaceTool) Name() string { return "google_maps_place" }

func (t *GoogleMapsPlaceTool) Description() string {
	return "Look up Google Maps place details using SerpApi Google Maps. Use this for Google Maps links, restaurant/place ratings, reviews, address, hours, phone, and coordinates. Requires SERPAPI_API_KEY."
}

func (t *GoogleMapsPlaceTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query":       map[string]any{"type": "string", "description": "Place name, Google Maps URL, or search query, e.g. Minh Quán Beer And Foods Đà Nẵng."},
			"max_results": map[string]any{"type": "integer", "description": "Max places to return, default 3, max 10."},
		},
		"required": []string{"query"},
	}
}

func (t *GoogleMapsPlaceTool) Execute(ctx context.Context, args map[string]any) *Result {
	apiKey, err := t.secrets.Get(ctx, "SERPAPI_API_KEY")
	if err != nil || strings.TrimSpace(apiKey) == "" {
		return ErrorResult("SERPAPI_API_KEY is not configured. Add a SerpApi key to enable Google Maps place lookup.")
	}
	query := strings.TrimSpace(fmt.Sprint(args["query"]))
	if query == "" || query == "<nil>" {
		return ErrorResult("query is required")
	}
	resolved := mapsResolvedQuery{Query: query}
	if mapsQuery, err := t.resolveGoogleMapsQuery(ctx, query); err == nil && mapsQuery.Query != "" {
		resolved = mapsQuery
	}
	maxResults := 3
	if v, ok := flightIntArg(args["max_results"]); ok && v > 0 {
		maxResults = min(v, 10)
	}

	values := url.Values{}
	values.Set("engine", "google_maps")
	values.Set("q", resolved.Query)
	if resolved.LL != "" {
		values.Set("ll", resolved.LL)
	}
	values.Set("hl", "vi")
	values.Set("gl", "vn")
	values.Set("api_key", apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serpAPIGoogleFlightsEndpoint+"?"+values.Encode(), nil)
	if err != nil {
		return ErrorResult(fmt.Sprintf("create Google Maps request: %v", err))
	}
	res, err := t.client.Do(req)
	if err != nil {
		return ErrorResult(fmt.Sprintf("Google Maps API request failed: %v", err))
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return ErrorResult(fmt.Sprintf("read Google Maps response: %v", err))
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return ErrorResult(fmt.Sprintf("Google Maps API returned HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(body))))
	}
	var parsed serpMapsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ErrorResult(fmt.Sprintf("parse Google Maps response: %v", err))
	}
	if parsed.Error != "" {
		return ErrorResult("Google Maps API error: " + parsed.Error)
	}
	return NewResult(formatMapsResults(query, resolved.Query, parsed.LocalResults, maxResults))
}

type mapsResolvedQuery struct {
	Query string
	LL    string
}

func (t *GoogleMapsPlaceTool) resolveGoogleMapsQuery(ctx context.Context, raw string) (mapsResolvedQuery, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return mapsResolvedQuery{}, nil
	}
	if !strings.Contains(u.Host, "google.") && u.Host != "maps.app.goo.gl" && u.Host != "goo.gl" {
		return mapsResolvedQuery{}, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return mapsResolvedQuery{}, err
	}
	res, err := t.client.Do(req)
	if err != nil {
		return mapsResolvedQuery{}, err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1024))
	return googleMapsQueryFromURL(res.Request.URL.String())
}

func googleMapsQueryFromURL(raw string) (mapsResolvedQuery, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return mapsResolvedQuery{}, err
	}
	resolved := mapsResolvedQuery{LL: googleMapsLLFromPath(u.EscapedPath())}
	parts := strings.Split(u.EscapedPath(), "/")
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] == "place" || parts[i] == "search" {
			name, err := url.PathUnescape(parts[i+1])
			if err != nil {
				return mapsResolvedQuery{}, err
			}
			name = strings.ReplaceAll(name, "+", " ")
			name = strings.TrimSpace(name)
			if name != "" {
				resolved.Query = name
				return resolved, nil
			}
		}
	}
	if q := strings.TrimSpace(u.Query().Get("q")); q != "" {
		resolved.Query = q
		return resolved, nil
	}
	return resolved, nil
}

var googleMapsAtCoordRe = regexp.MustCompile(`@(-?\d+(?:\.\d+)?),(-?\d+(?:\.\d+)?),(\d+(?:\.\d+)?z)`)

func googleMapsLLFromPath(path string) string {
	m := googleMapsAtCoordRe.FindStringSubmatch(path)
	if len(m) != 4 {
		return ""
	}
	return fmt.Sprintf("@%s,%s,%s", m[1], m[2], m[3])
}

type serpMapsResponse struct {
	Error        string           `json:"error"`
	LocalResults []serpMapsResult `json:"local_results"`
}

type serpMapsResult struct {
	Position       int                `json:"position"`
	Title          string             `json:"title"`
	Rating         float64            `json:"rating"`
	Reviews        int                `json:"reviews"`
	Price          string             `json:"price"`
	Type           string             `json:"type"`
	Address        string             `json:"address"`
	Hours          string             `json:"hours"`
	OpenState      string             `json:"open_state"`
	Phone          string             `json:"phone"`
	UserReview     string             `json:"user_review"`
	GPSCoordinates map[string]float64 `json:"gps_coordinates"`
}

func formatMapsResults(originalQuery, resolvedQuery string, results []serpMapsResult, maxResults int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Kết quả Google Maps cho: %s\n", originalQuery)
	if resolvedQuery != "" && resolvedQuery != originalQuery {
		fmt.Fprintf(&b, "Truy vấn đã resolve: %s\n", resolvedQuery)
	}
	b.WriteString("\n")
	if len(results) == 0 {
		b.WriteString("Không tìm thấy địa điểm phù hợp trên Google Maps.\n")
		return b.String()
	}
	if len(results) > maxResults {
		results = results[:maxResults]
	}
	b.WriteString("| # | Tên | Loại | Đánh giá | Địa chỉ | Giờ | Điện thoại |\n")
	b.WriteString("|---:|---|---|---:|---|---|---|\n")
	for _, r := range results {
		rating := "-"
		if r.Rating > 0 {
			rating = fmt.Sprintf("%.1f (%d review)", r.Rating, r.Reviews)
		}
		hours := r.Hours
		if hours == "" {
			hours = r.OpenState
		}
		fmt.Fprintf(&b, "| %d | %s | %s | %s | %s | %s | %s |\n", r.Position, escapeTable(r.Title), escapeTable(r.Type), escapeTable(rating), escapeTable(r.Address), escapeTable(hours), escapeTable(r.Phone))
		if r.Price != "" || r.UserReview != "" {
			fmt.Fprintf(&b, "|  |  |  |  | %s |  | %s |\n", escapeTable(r.Price), escapeTable(r.UserReview))
		}
	}
	return b.String()
}
