package tools

import "testing"

func TestWeatherCodeText(t *testing.T) {
	if got := weatherCodeText(0); got != "clear" {
		t.Fatalf("weatherCodeText(0) = %q", got)
	}
	if got := weatherCodeText(80); got != "rain showers" {
		t.Fatalf("weatherCodeText(80) = %q", got)
	}
	if got := weatherCodeText(95); got != "thunderstorm" {
		t.Fatalf("weatherCodeText(95) = %q", got)
	}
}

func TestSummarizeHourlyCapsAt24(t *testing.T) {
	h := weatherHourlyBlock{}
	for i := 0; i < 30; i++ {
		h.Time = append(h.Time, "2026-04-30T00:00")
		h.Temperature2m = append(h.Temperature2m, float64(i))
		h.PrecipitationProbability = append(h.PrecipitationProbability, i)
	}
	got := summarizeHourly(h, "today")
	if len(got) != 24 {
		t.Fatalf("len(summarizeHourly) = %d, want 24", len(got))
	}
}

func TestSummarizeHourlyTomorrow(t *testing.T) {
	h := weatherHourlyBlock{
		Time:          []string{"2026-04-30T23:00", "2026-05-01T00:00", "2026-05-01T01:00"},
		Temperature2m: []float64{30, 25, 26},
	}
	got := summarizeHourly(h, "tomorrow")
	if len(got) != 2 {
		t.Fatalf("len(summarizeHourly tomorrow) = %d, want 2", len(got))
	}
	if got[0]["time"] != "2026-05-01T00:00" {
		t.Fatalf("first tomorrow time = %v", got[0]["time"])
	}
}
