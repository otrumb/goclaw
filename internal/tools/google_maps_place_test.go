package tools

import (
	"reflect"
	"testing"
)

func TestGoogleMapsFallbackQueries(t *testing.T) {
	got := googleMapsFallbackQueries("Gu Quán - Nhậu Chill Đà Nẵng")
	want := []string{"Gu Quán Đà Nẵng", "Gu Quán", "Gu Quán Nhậu Chill Đà Nẵng"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("googleMapsFallbackQueries() = %#v, want %#v", got, want)
	}
}

func TestGoogleMapsFallbackQueriesNoDuplicate(t *testing.T) {
	got := googleMapsFallbackQueries("Minh Quán")
	if len(got) != 0 {
		t.Fatalf("expected no fallback for simple query, got %#v", got)
	}
}

func TestGoogleMapsFallbackQueriesQuotedQuery(t *testing.T) {
	got := googleMapsFallbackQueries(`"Gu Quán" "Nhậu Chill" Đà Nẵng`)
	want := []string{"Gu Quán Đà Nẵng", "Gu Quán"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("googleMapsFallbackQueries() = %#v, want %#v", got, want)
	}
}

func TestGoogleMapsLLFromMeterPath(t *testing.T) {
	got := googleMapsLLFromPath("/maps/place/Gu+Qu%C3%A1n/@16.0361226,108.2321688,110m/data=!3m1")
	if got != "@16.0361226,108.2321688,18z" {
		t.Fatalf("googleMapsLLFromPath() = %q", got)
	}
}
