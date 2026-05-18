package bus

import (
	"testing"
	"time"
)

func TestInboundDebouncerSeparatesDifferentReplyTargets(t *testing.T) {
	var flushed []InboundMessage
	d := NewInboundDebouncer(time.Hour, func(msg InboundMessage) {
		flushed = append(flushed, msg)
	})

	d.Push(InboundMessage{
		Channel:  "smartca-care-bot",
		ChatID:   "-5060265610",
		SenderID: "123",
		Content:  "mention first",
		Metadata: map[string]string{"origin_reply_to_message_id": "1001"},
	})
	d.Push(InboundMessage{
		Channel:  "smartca-care-bot",
		ChatID:   "-5060265610",
		SenderID: "123",
		Content:  "mention second",
		Metadata: map[string]string{"origin_reply_to_message_id": "1002"},
	})
	d.Stop()

	if len(flushed) != 2 {
		t.Fatalf("expected different reply targets to flush separately, got %d: %#v", len(flushed), flushed)
	}
	if flushed[0].Content == "mention first\nmention second" || flushed[1].Content == "mention first\nmention second" {
		t.Fatalf("expected messages not to be merged, got %#v", flushed)
	}
}

func TestInboundDebouncerMergesSameReplyTarget(t *testing.T) {
	var flushed []InboundMessage
	d := NewInboundDebouncer(time.Hour, func(msg InboundMessage) {
		flushed = append(flushed, msg)
	})

	base := InboundMessage{
		Channel:  "smartca-care-bot",
		ChatID:   "-5060265610",
		SenderID: "123",
		Metadata: map[string]string{"origin_reply_to_message_id": "1001"},
	}
	first := base
	first.Content = "mention first"
	second := base
	second.Content = "mention second"
	d.Push(first)
	d.Push(second)
	d.Stop()

	if len(flushed) != 1 {
		t.Fatalf("expected same reply target to merge, got %d: %#v", len(flushed), flushed)
	}
	if flushed[0].Content != "mention first\nmention second" {
		t.Fatalf("unexpected merged content: %q", flushed[0].Content)
	}
}
