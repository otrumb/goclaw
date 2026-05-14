package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

const serpAPIGoogleFlightsEndpoint = "https://serpapi.com/search.json"

type FlightFaresTool struct {
	secrets store.ConfigSecretsStore
	client  *http.Client
}

func NewFlightFaresTool(secrets store.ConfigSecretsStore) *FlightFaresTool {
	return &FlightFaresTool{secrets: secrets, client: &http.Client{Timeout: 25 * time.Second}}
}

func (t *FlightFaresTool) Name() string { return "flight_fares" }

func (t *FlightFaresTool) Description() string {
	return "Search live flight fares using a structured flight-pricing API. Use this for airfare tables and route price comparisons instead of generic web_search. Requires SERPAPI_API_KEY."
}

func (t *FlightFaresTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"from":        map[string]any{"type": "string", "description": "Origin airport IATA code, e.g. DAD."},
			"to":          map[string]any{"type": "string", "description": "Destination airport IATA code, e.g. SGN."},
			"date":        map[string]any{"type": "string", "description": "Outbound date in YYYY-MM-DD local format."},
			"return_date": map[string]any{"type": "string", "description": "Optional return date in YYYY-MM-DD for round trip."},
			"adults":      map[string]any{"type": "integer", "description": "Passenger count. Default 1."},
			"currency":    map[string]any{"type": "string", "description": "Currency code. Default VND."},
			"max_results": map[string]any{"type": "integer", "description": "Maximum rows to return. Default 8, max 20."},
		},
		"required": []string{"from", "to", "date"},
	}
}

func (t *FlightFaresTool) Execute(ctx context.Context, args map[string]any) *Result {
	if t.secrets == nil {
		return ErrorResult("flight fare API secrets store is not available")
	}
	apiKey, err := t.secrets.Get(ctx, "SERPAPI_API_KEY")
	if err != nil || strings.TrimSpace(apiKey) == "" {
		return ErrorResult("SERPAPI_API_KEY is not configured. Add a SerpApi key to config_secrets with key SERPAPI_API_KEY to enable live Google Flights fare tables.")
	}

	from := strings.ToUpper(strings.TrimSpace(fmt.Sprint(args["from"])))
	to := strings.ToUpper(strings.TrimSpace(fmt.Sprint(args["to"])))
	date := strings.TrimSpace(fmt.Sprint(args["date"]))
	returnDate := strings.TrimSpace(fmt.Sprint(args["return_date"]))
	currency := strings.ToUpper(strings.TrimSpace(fmt.Sprint(args["currency"])))
	if currency == "" || currency == "<NIL>" {
		currency = "VND"
	}
	if from == "" || from == "<NIL>" || to == "" || to == "<NIL>" || date == "" || date == "<nil>" {
		return ErrorResult("from, to, and date are required. Use IATA airport codes and date YYYY-MM-DD, e.g. from=DAD to=SGN date=2026-05-08")
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return ErrorResult("date must be YYYY-MM-DD")
	}
	if returnDate != "" && returnDate != "<nil>" {
		if _, err := time.Parse("2006-01-02", returnDate); err != nil {
			return ErrorResult("return_date must be YYYY-MM-DD")
		}
	}
	adults := 1
	if v, ok := flightIntArg(args["adults"]); ok && v > 0 {
		adults = min(v, 9)
	}
	maxResults := 8
	if v, ok := flightIntArg(args["max_results"]); ok && v > 0 {
		maxResults = min(v, 20)
	}

	resp, err := t.searchSerpAPI(ctx, apiKey, flightFareRequest{From: from, To: to, Date: date, ReturnDate: returnDate, Adults: adults, Currency: currency})
	if err != nil {
		return ErrorResult(err.Error())
	}
	rows := flattenFlightFareRows(resp)
	sort.Slice(rows, func(i, j int) bool { return rows[i].PriceValue < rows[j].PriceValue })
	if len(rows) > maxResults {
		rows = rows[:maxResults]
	}
	out := formatFlightFareTable(from, to, date, returnDate, currency, rows)
	return NewResult(out)
}

type flightFareRequest struct {
	From, To, Date, ReturnDate, Currency string
	Adults                               int
}

func (t *FlightFaresTool) searchSerpAPI(ctx context.Context, apiKey string, req flightFareRequest) (*serpFlightResponse, error) {
	values := url.Values{}
	values.Set("engine", "google_flights")
	values.Set("departure_id", req.From)
	values.Set("arrival_id", req.To)
	values.Set("outbound_date", req.Date)
	values.Set("currency", req.Currency)
	values.Set("hl", "vi")
	values.Set("gl", "vn")
	values.Set("adults", fmt.Sprint(req.Adults))
	values.Set("api_key", apiKey)
	if req.ReturnDate != "" && req.ReturnDate != "<nil>" {
		values.Set("type", "1")
		values.Set("return_date", req.ReturnDate)
	} else {
		values.Set("type", "2")
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, serpAPIGoogleFlightsEndpoint+"?"+values.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("create flight fare request: %w", err)
	}
	res, err := t.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("flight fare API request failed: %w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read flight fare response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("flight fare API returned HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	var parsed serpFlightResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse flight fare response: %w", err)
	}
	if parsed.Error != "" {
		return nil, fmt.Errorf("flight fare API error: %s", parsed.Error)
	}
	return &parsed, nil
}

type serpFlightResponse struct {
	Error        string             `json:"error"`
	BestFlights  []serpFlightOption `json:"best_flights"`
	OtherFlights []serpFlightOption `json:"other_flights"`
}

type serpFlightOption struct {
	Flights       []serpFlightLeg `json:"flights"`
	Price         int             `json:"price"`
	TotalDuration int             `json:"total_duration"`
}

type serpFlightLeg struct {
	Airline          string          `json:"airline"`
	FlightNumber     string          `json:"flight_number"`
	DepartureAirport serpFlightPoint `json:"departure_airport"`
	ArrivalAirport   serpFlightPoint `json:"arrival_airport"`
}

type serpFlightPoint struct {
	Name string `json:"name"`
	Time string `json:"time"`
}

type flightFareRow struct {
	Airline, FlightNumber, Depart, Arrive string
	DurationMin, PriceValue               int
}

func flattenFlightFareRows(resp *serpFlightResponse) []flightFareRow {
	var rows []flightFareRow
	for _, opt := range append(resp.BestFlights, resp.OtherFlights...) {
		if len(opt.Flights) == 0 || opt.Price <= 0 {
			continue
		}
		first := opt.Flights[0]
		last := opt.Flights[len(opt.Flights)-1]
		airline := first.Airline
		if len(opt.Flights) > 1 {
			airline += fmt.Sprintf(" + %d chặng", len(opt.Flights)-1)
		}
		rows = append(rows, flightFareRow{
			Airline:      airline,
			FlightNumber: first.FlightNumber,
			Depart:       first.DepartureAirport.Time,
			Arrive:       last.ArrivalAirport.Time,
			DurationMin:  opt.TotalDuration,
			PriceValue:   opt.Price,
		})
	}
	return rows
}

func formatFlightFareTable(from, to, date, returnDate, currency string, rows []flightFareRow) string {
	trip := "một chiều"
	if returnDate != "" && returnDate != "<nil>" {
		trip = "khứ hồi"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Bảng giá vé máy bay %s → %s (%s, %s):\n\n", from, to, date, trip)
	if len(rows) == 0 {
		b.WriteString("Không thấy chuyến bay/giá trong phản hồi API cho tiêu chí này. Hãy thử ngày khác hoặc kiểm tra lại mã sân bay.\n")
		return b.String()
	}
	b.WriteString("| Hãng | Số hiệu | Giờ đi | Giờ đến | Thời lượng | Giá |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n",
			escapeTable(row.Airline), escapeTable(row.FlightNumber), escapeTable(row.Depart), escapeTable(row.Arrive), formatDurationMinutes(row.DurationMin), formatFlightPrice(row.PriceValue, currency))
	}
	b.WriteString("\nLưu ý: giá phụ thuộc hành lý, thuế/phí, hạng vé và tình trạng còn chỗ tại thời điểm thanh toán.\n")
	return b.String()
}

func formatDurationMinutes(minutes int) string {
	if minutes <= 0 {
		return "-"
	}
	return fmt.Sprintf("%dh%02d", minutes/60, minutes%60)
}

func formatFlightPrice(price int, currency string) string {
	if price <= 0 {
		return "-"
	}
	s := fmt.Sprintf("%d", price)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "." + s[i:]
	}
	return s + " " + currency
}

func escapeTable(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "|", "\\|")
}

func flightIntArg(v any) (int, bool) {
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
