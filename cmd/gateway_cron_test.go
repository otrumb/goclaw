package cmd

import "testing"

func TestCronRunToolAllowExcludesCron(t *testing.T) {
	for _, name := range cronRunToolAllow() {
		if name == "cron" {
			t.Fatal("cronRunToolAllow must not include cron")
		}
	}
}

func TestCronRunToolAllowIncludesSafeRealtimeTools(t *testing.T) {
	got := map[string]bool{}
	for _, name := range cronRunToolAllow() {
		got[name] = true
	}
	for _, want := range []string{"datetime", "web_search", "web_fetch", "weather"} {
		if !got[want] {
			t.Fatalf("cronRunToolAllow missing %s", want)
		}
	}
}
