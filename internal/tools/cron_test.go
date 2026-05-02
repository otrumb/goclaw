package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAdjustNearPastAtMS(t *testing.T) {
	now := int64(1_000_000)
	adjusted, ok := adjustNearPastAtMS(now-30_000, now)
	if !ok {
		t.Fatal("near-past atMs should be adjusted")
	}
	if adjusted != now+60_000 {
		t.Fatalf("adjusted = %d, want %d", adjusted, now+60_000)
	}
}

func TestAdjustNearPastAtMSRejectsFarPast(t *testing.T) {
	now := int64(1_000_000)
	if _, ok := adjustNearPastAtMS(now-int64(11*time.Minute/time.Millisecond), now); ok {
		t.Fatal("far-past atMs should not be adjusted")
	}
	if _, ok := adjustNearPastAtMS(0, now); ok {
		t.Fatal("zero atMs should not be adjusted")
	}
}

func TestDuplicateCronJobNameError(t *testing.T) {
	err := errors.New(`create cron job: ERROR: duplicate key value violates unique constraint "uq_cron_jobs_agent_tenant_name"`)
	if !isDuplicateCronJobNameError(err) {
		t.Fatal("duplicate cron job name error was not detected")
	}
	if isDuplicateCronJobNameError(errors.New("duplicate unrelated value")) {
		t.Fatal("unrelated duplicate error should not match")
	}
}

func TestUniqueCronJobName(t *testing.T) {
	got := uniqueCronJobName("nhac-sau-1-phut", time.Date(2026, 5, 1, 10, 30, 45, 0, time.UTC))
	if got != "nhac-sau-1-phut-20260501-103045" {
		t.Fatalf("uniqueCronJobName() = %q", got)
	}
	long := strings.Repeat("a", 200)
	if len(uniqueCronJobName(long, time.Date(2026, 5, 1, 10, 30, 45, 0, time.UTC))) > 120 {
		t.Fatal("uniqueCronJobName should cap long names")
	}
}

func TestCronInternalChannelNames(t *testing.T) {
	for _, ch := range []string{"cli", "system", "subagent", "cron", "teammate", "http", "api", "wake"} {
		if !isInternalCronChannel(ch) {
			t.Fatalf("isInternalCronChannel(%q) = false, want true", ch)
		}
	}
	if isInternalCronChannel("otrumai-bot") {
		t.Fatal("telegram channel instance should not be treated as internal")
	}
}

func TestNormalizeCronTimezone(t *testing.T) {
	if got := normalizeCronTimezone("Asia/Ho_Chi_Minh"); got != "Asia/Saigon" {
		t.Fatalf("normalizeCronTimezone = %q, want Asia/Saigon", got)
	}
}

func TestParseAtLocalMS(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Saigon")
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseAtLocalMS("2026-05-02T13:00:00", loc)
	if err != nil {
		t.Fatalf("parseAtLocalMS returned error: %v", err)
	}
	want := time.Date(2026, 5, 2, 13, 0, 0, 0, loc).UnixMilli()
	if got != want {
		t.Fatalf("parseAtLocalMS = %d (%s), want %d (%s)", got, time.UnixMilli(got), want, time.UnixMilli(want))
	}
}

func TestDateTimeDefaultsToAsiaSaigon(t *testing.T) {
	res := NewDateTimeTool().Execute(context.Background(), map[string]any{})
	if res.IsError {
		t.Fatalf("datetime returned error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, `"timezone": "Asia/Saigon"`) {
		t.Fatalf("datetime default timezone missing: %s", res.ForLLM)
	}
}
