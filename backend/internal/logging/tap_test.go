package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestTapReceivesEveryRecord(t *testing.T) {
	var mu sync.Mutex
	var captured []string

	var buf bytes.Buffer
	base := slog.NewTextHandler(&buf, nil)
	h := &tapHandler{
		next: base,
		tap: func(_ context.Context, r slog.Record) {
			mu.Lock()
			defer mu.Unlock()
			captured = append(captured, r.Message)
		},
	}

	logger := slog.New(h)
	logger.Info("first")
	logger.Warn("second")

	mu.Lock()
	defer mu.Unlock()
	if len(captured) != 2 || captured[0] != "first" || captured[1] != "second" {
		t.Fatalf("captured = %v, want [first second]", captured)
	}
	// The wrapped handler must still receive every record unchanged.
	if !strings.Contains(buf.String(), "first") || !strings.Contains(buf.String(), "second") {
		t.Errorf("underlying handler output missing expected messages: %s", buf.String())
	}
}

func TestTapSurvivesPanic(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewTextHandler(&buf, nil)
	h := &tapHandler{
		next: base,
		tap:  func(_ context.Context, _ slog.Record) { panic("boom") },
	}

	logger := slog.New(h)
	// Must not panic and must still reach the underlying handler.
	logger.Info("still logged")

	if !strings.Contains(buf.String(), "still logged") {
		t.Errorf("expected underlying handler to still receive the record despite a panicking Tap")
	}
}

func TestTapWithAttrsStillTaps(t *testing.T) {
	var count int
	var mu sync.Mutex

	base := slog.NewTextHandler(&bytes.Buffer{}, nil)
	h := &tapHandler{
		next: base,
		tap: func(_ context.Context, _ slog.Record) {
			mu.Lock()
			count++
			mu.Unlock()
		},
	}

	logger := slog.New(h).With("component", "test")
	logger.Info("via derived handler")

	mu.Lock()
	defer mu.Unlock()
	if count != 1 {
		t.Errorf("count = %d, want 1 (derived handler from WithAttrs must still tap)", count)
	}
}
