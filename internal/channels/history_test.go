package channels

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestNormalizeGroupHistoryLimitCapsAt15(t *testing.T) {
	if got := NormalizeGroupHistoryLimit(200); got != 15 {
		t.Fatalf("NormalizeGroupHistoryLimit(200) = %d, want 15", got)
	}
	if got := NormalizeGroupHistoryLimit(0); got != 0 {
		t.Fatalf("NormalizeGroupHistoryLimit(0) = %d, want 0", got)
	}
}

func TestBuildContextCapsHistoryAt15Messages(t *testing.T) {
	ph := NewPendingHistory()
	for i := 0; i < 20; i++ {
		ph.Record("group", HistoryEntry{Sender: "u", Body: fmt.Sprintf("msg-%02d", i), Timestamp: time.Now()}, 200)
	}
	out := ph.BuildContext("group", "hello", 200)
	if strings.Contains(out, "msg-04") {
		t.Fatalf("expected old messages to be trimmed, got: %s", out)
	}
	if !strings.Contains(out, "msg-05") || !strings.Contains(out, "msg-19") {
		t.Fatalf("expected latest 15 messages, got: %s", out)
	}
}

func TestBuildContextIsolatesStrongToolIntent(t *testing.T) {
	ph := NewPendingHistory()
	ph.Record("group", HistoryEntry{Sender: "u", Body: "Minh Quán nhậu ngon", Timestamp: time.Now()}, 15)
	out := ph.BuildContext("group", "tạo ảnh quảng cáo S26 Ultra", 15)
	if strings.Contains(out, "Minh Quán") {
		t.Fatalf("expected unrelated history to be excluded, got: %s", out)
	}
	if out != "tạo ảnh quảng cáo S26 Ultra" {
		t.Fatalf("expected current message only, got: %s", out)
	}
}

func TestBuildContextSmartCACancelIntentDoesNotIncludeUnrelatedUIDHistory(t *testing.T) {
	ph := NewPendingHistory()
	ph.Record("group", HistoryEntry{Sender: "u", Body: "uid: 004191004004 ktra ton don", Timestamp: time.Now()}, 15)
	out := ph.BuildContext("group", "[From: Dai ka]\nho tro huy don hang Doi thiet bi CCCD: 036076024086", 15)
	if strings.Contains(out, "004191004004") {
		t.Fatalf("expected unrelated UID history to be excluded, got: %s", out)
	}
	if out != "[From: Dai ka]\nho tro huy don hang Doi thiet bi CCCD: 036076024086" {
		t.Fatalf("expected current message only, got: %s", out)
	}
}
