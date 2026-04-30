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

// WeatherTool returns current and same-day hourly weather from Open-Meteo.
type WeatherTool struct {
	client *http.Client
}

func NewWeatherTool() *WeatherTool {
	return &WeatherTool{client: &http.Client{Timeout: 12 * time.Second}}
}

func (t *WeatherTool) Name() string { return "weather" }

func (t *WeatherTool) Description() string {
	return "Get current weather and today's hourly forecast for a location using Open-Meteo structured JSON. Use this for weather, temperature, rain risk, and hourly forecast questions instead of web_search snippets."
}

func (t *WeatherTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"location": map[string]any{
				"type":        "string",
				"description": "City/location name, e.g. 'Đà Nẵng', 'Hà Nội', 'Ho Chi Minh City'.",
			},
			"hourly": map[string]any{
				"type":        "boolean",
				"description": "Whether to include hourly temperature/rain probability. Use true for 'theo giờ/hourly'. Default false when forecast_days > 2, otherwise true.",
			},
			"day": map[string]any{
				"type":        "string",
				"enum":        []string{"today", "tomorrow"},
				"description": "Optional single-day focus. Use 'tomorrow' for ngày mai/tomorrow. Omit for multi-day forecasts.",
			},
			"forecast_days": map[string]any{
				"type":        "integer",
				"description": "Number of forecast days to request from Open-Meteo, 1-16. Use 7 for '7 ngày tới', 10 for '10 ngày tới'. Default 2 for tomorrow, otherwise 1.",
			},
		},
		"required": []string{"location"},
	}
}

func (t *WeatherTool) Execute(ctx context.Context, args map[string]any) *Result {
	location := strings.TrimSpace(fmt.Sprint(args["location"]))
	if location == "" || location == "<nil>" {
		return ErrorResult("location is required, e.g. 'Đà Nẵng'")
	}
	day := strings.ToLower(strings.TrimSpace(fmt.Sprint(args["day"])))
	if day == "" || day == "<nil>" {
		day = ""
	}
	if day != "" && day != "today" && day != "tomorrow" {
		return ErrorResult("day must be 'today' or 'tomorrow'")
	}
	forecastDays := 1
	if day == "tomorrow" {
		forecastDays = 2
	}
	if v, ok := weatherIntArg(args["forecast_days"]); ok {
		forecastDays = max(1, min(16, v))
	}
	hourly := forecastDays <= 2
	if v, ok := args["hourly"].(bool); ok {
		hourly = v
	}

	geo, err := t.geocode(ctx, location)
	if err != nil {
		return ErrorResult(err.Error())
	}
	forecast, err := t.forecast(ctx, geo, forecastDays)
	if err != nil {
		return ErrorResult(err.Error())
	}

	out := map[string]any{
		"source":   "Open-Meteo",
		"location": geo,
		"current":  forecast.Current,
		"daily":    forecast.Daily.WithConditions(),
		"units":    forecast.CurrentUnits,
		"timezone": forecast.Timezone,
		"requested": map[string]any{
			"day":           day,
			"forecast_days": forecastDays,
			"hourly":        hourly,
		},
	}
	if hourly {
		focus := day
		if focus == "" {
			focus = "today"
		}
		out["hourly"] = summarizeHourly(forecast.Hourly, focus)
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	return NewResult(string(data))
}

type weatherGeoResponse struct {
	Results []weatherLocation `json:"results"`
}

type weatherLocation struct {
	Name      string  `json:"name"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Country   string  `json:"country"`
	Admin1    string  `json:"admin1"`
	Timezone  string  `json:"timezone"`
}

type weatherForecastResponse struct {
	Timezone     string             `json:"timezone"`
	CurrentUnits map[string]string  `json:"current_units"`
	Current      weatherCurrent     `json:"current"`
	Hourly       weatherHourlyBlock `json:"hourly"`
	Daily        weatherDailyBlock  `json:"daily"`
}

type weatherCurrent struct {
	Time             string  `json:"time"`
	Temperature2m    float64 `json:"temperature_2m"`
	RelativeHumidity int     `json:"relative_humidity_2m"`
	ApparentTemp     float64 `json:"apparent_temperature"`
	Precipitation    float64 `json:"precipitation"`
	Rain             float64 `json:"rain"`
	WeatherCode      int     `json:"weather_code"`
	CloudCover       int     `json:"cloud_cover"`
	WindSpeed10m     float64 `json:"wind_speed_10m"`
	Condition        string  `json:"condition"`
}

type weatherHourlyBlock struct {
	Time                     []string  `json:"time"`
	Temperature2m            []float64 `json:"temperature_2m"`
	PrecipitationProbability []int     `json:"precipitation_probability"`
	Precipitation            []float64 `json:"precipitation"`
	Rain                     []float64 `json:"rain"`
	WeatherCode              []int     `json:"weather_code"`
}

type weatherDailyBlock struct {
	Time                        []string  `json:"time"`
	WeatherCode                 []int     `json:"weather_code"`
	Temperature2mMax            []float64 `json:"temperature_2m_max"`
	Temperature2mMin            []float64 `json:"temperature_2m_min"`
	PrecipitationSum            []float64 `json:"precipitation_sum"`
	RainSum                     []float64 `json:"rain_sum"`
	PrecipitationProbabilityMax []int     `json:"precipitation_probability_max"`
}

func (d weatherDailyBlock) WithConditions() []map[string]any {
	limit := len(d.Time)
	out := make([]map[string]any, 0, limit)
	for i := 0; i < limit; i++ {
		row := map[string]any{"date": d.Time[i]}
		if i < len(d.WeatherCode) {
			row["condition"] = weatherCodeText(d.WeatherCode[i])
			row["weather_code"] = d.WeatherCode[i]
		}
		if i < len(d.Temperature2mMax) {
			row["temperature_2m_max"] = d.Temperature2mMax[i]
		}
		if i < len(d.Temperature2mMin) {
			row["temperature_2m_min"] = d.Temperature2mMin[i]
		}
		if i < len(d.PrecipitationProbabilityMax) {
			row["precipitation_probability_max"] = d.PrecipitationProbabilityMax[i]
		}
		if i < len(d.RainSum) {
			row["rain_sum"] = d.RainSum[i]
		}
		if i < len(d.PrecipitationSum) {
			row["precipitation_sum"] = d.PrecipitationSum[i]
		}
		out = append(out, row)
	}
	return out
}

func (t *WeatherTool) geocode(ctx context.Context, location string) (weatherLocation, error) {
	u := "https://geocoding-api.open-meteo.com/v1/search?name=" + url.QueryEscape(location) + "&count=1&language=vi&format=json"
	var resp weatherGeoResponse
	if err := t.getJSON(ctx, u, &resp); err != nil {
		return weatherLocation{}, fmt.Errorf("weather geocoding failed: %w", err)
	}
	if len(resp.Results) == 0 {
		return weatherLocation{}, fmt.Errorf("no weather location found for %q", location)
	}
	return resp.Results[0], nil
}

func (t *WeatherTool) forecast(ctx context.Context, loc weatherLocation, forecastDays int) (weatherForecastResponse, error) {
	u := fmt.Sprintf("https://api.open-meteo.com/v1/forecast?latitude=%g&longitude=%g&current=temperature_2m,relative_humidity_2m,apparent_temperature,precipitation,rain,weather_code,cloud_cover,wind_speed_10m&hourly=temperature_2m,precipitation_probability,precipitation,rain,weather_code&daily=weather_code,temperature_2m_max,temperature_2m_min,precipitation_sum,rain_sum,precipitation_probability_max&forecast_days=%d&timezone=auto", loc.Latitude, loc.Longitude, forecastDays)
	var resp weatherForecastResponse
	if err := t.getJSON(ctx, u, &resp); err != nil {
		return weatherForecastResponse{}, fmt.Errorf("weather forecast failed: %w", err)
	}
	resp.Current.Condition = weatherCodeText(resp.Current.WeatherCode)
	return resp, nil
}

func (t *WeatherTool) getJSON(ctx context.Context, rawURL string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	res, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(res.Body).Decode(dst)
}

func summarizeHourly(h weatherHourlyBlock, day string) []map[string]any {
	start, end := hourlyWindow(h.Time, day)
	if start < 0 || end <= start {
		return nil
	}
	out := make([]map[string]any, 0, min(24, end-start))
	limit := min(end, start+24)
	for i := start; i < limit; i++ {
		row := map[string]any{"time": h.Time[i]}
		if i < len(h.Temperature2m) {
			row["temperature_2m"] = h.Temperature2m[i]
		}
		if i < len(h.PrecipitationProbability) {
			row["precipitation_probability"] = h.PrecipitationProbability[i]
		}
		if i < len(h.Rain) {
			row["rain"] = h.Rain[i]
		}
		if i < len(h.WeatherCode) {
			row["condition"] = weatherCodeText(h.WeatherCode[i])
		}
		out = append(out, row)
	}
	return out
}

func hourlyWindow(times []string, day string) (int, int) {
	if len(times) == 0 {
		return -1, -1
	}
	date := ""
	if day == "tomorrow" {
		first := datePart(times[0])
		for _, ts := range times {
			if d := datePart(ts); d != "" && d != first {
				date = d
				break
			}
		}
	} else {
		date = datePart(times[0])
	}
	if date == "" {
		return -1, -1
	}
	start := -1
	end := len(times)
	for i, ts := range times {
		if datePart(ts) == date {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			end = i
			break
		}
	}
	return start, end
}

func datePart(ts string) string {
	if len(ts) < 10 {
		return ""
	}
	return ts[:10]
}

func weatherIntArg(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}

func weatherCodeText(code int) string {
	switch code {
	case 0:
		return "clear"
	case 1, 2, 3:
		return "partly cloudy/cloudy"
	case 45, 48:
		return "fog"
	case 51, 53, 55, 56, 57:
		return "drizzle"
	case 61, 63, 65, 66, 67:
		return "rain"
	case 80, 81, 82:
		return "rain showers"
	case 95, 96, 99:
		return "thunderstorm"
	default:
		return "unknown"
	}
}
