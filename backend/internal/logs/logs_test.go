package logs

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"weloury/internal/eventbus"
)

func record(level slog.Level, msg string) slog.Record {
	return slog.NewRecord(time.Now(), level, msg, 0)
}

func TestCaptureAndQueryNewestFirst(t *testing.T) {
	e := New(nil, 0)
	e.Capture(context.Background(), record(slog.LevelInfo, "first"))
	e.Capture(context.Background(), record(slog.LevelInfo, "second"))
	e.Capture(context.Background(), record(slog.LevelInfo, "third"))

	got := e.Query(Query{})
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}
	if got[0].Message != "third" || got[1].Message != "second" || got[2].Message != "first" {
		t.Errorf("got = %+v, want newest-first [third second first]", got)
	}
	if got[0].Seq <= got[1].Seq {
		t.Errorf("expected Seq to increase with capture order")
	}
}

func TestCaptureEvictsOldestBeyondCapacity(t *testing.T) {
	e := New(nil, 3)
	for i, msg := range []string{"a", "b", "c", "d"} {
		_ = i
		e.Capture(context.Background(), record(slog.LevelInfo, msg))
	}

	got := e.Query(Query{Limit: 10})
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3 (capacity-bounded)", len(got))
	}
	// "a" should have been evicted; newest-first order is d, c, b.
	want := []string{"d", "c", "b"}
	for i, w := range want {
		if got[i].Message != w {
			t.Errorf("got[%d].Message = %q, want %q", i, got[i].Message, w)
		}
	}
}

func TestQueryFiltersByMinimumLevel(t *testing.T) {
	e := New(nil, 0)
	e.Capture(context.Background(), record(slog.LevelDebug, "debug msg"))
	e.Capture(context.Background(), record(slog.LevelInfo, "info msg"))
	e.Capture(context.Background(), record(slog.LevelWarn, "warn msg"))
	e.Capture(context.Background(), record(slog.LevelError, "error msg"))

	got := e.Query(Query{Level: "WARN"})
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2 (warn + error)", len(got))
	}
	for _, entry := range got {
		if entry.Level != "WARN" && entry.Level != "ERROR" {
			t.Errorf("unexpected level in filtered results: %s", entry.Level)
		}
	}
}

func TestQueryFiltersBySearchCaseInsensitive(t *testing.T) {
	e := New(nil, 0)
	e.Capture(context.Background(), record(slog.LevelInfo, "database connection established"))
	e.Capture(context.Background(), record(slog.LevelInfo, "listening on port 8080"))

	got := e.Query(Query{Search: "DATABASE"})
	if len(got) != 1 || got[0].Message != "database connection established" {
		t.Fatalf("got = %+v, want one match for case-insensitive search", got)
	}
}

func TestQueryPaginatesWithBeforeCursor(t *testing.T) {
	e := New(nil, 0)
	for _, msg := range []string{"1", "2", "3", "4", "5"} {
		e.Capture(context.Background(), record(slog.LevelInfo, msg))
	}

	firstPage := e.Query(Query{Limit: 2})
	if len(firstPage) != 2 || firstPage[0].Message != "5" || firstPage[1].Message != "4" {
		t.Fatalf("firstPage = %+v, want [5 4]", firstPage)
	}

	secondPage := e.Query(Query{Limit: 2, Before: firstPage[len(firstPage)-1].Seq})
	if len(secondPage) != 2 || secondPage[0].Message != "3" || secondPage[1].Message != "2" {
		t.Fatalf("secondPage = %+v, want [3 2]", secondPage)
	}
}

func TestQueryLimitClampedToCapacity(t *testing.T) {
	e := New(nil, 10)
	for i := 0; i < 10; i++ {
		e.Capture(context.Background(), record(slog.LevelInfo, "x"))
	}
	got := e.Query(Query{Limit: 1_000_000})
	if len(got) != 10 {
		t.Fatalf("len(got) = %d, want 10 (bounded by actual entries/capacity)", len(got))
	}
}

func TestCapturePublishesToBus(t *testing.T) {
	bus := eventbus.New()
	defer bus.Close()
	ch, cancel := bus.Subscribe(context.Background(), eventbus.MatchTypes(TypeEntry), 0)
	defer cancel()

	e := New(bus, 0)
	e.Capture(context.Background(), record(slog.LevelWarn, "hello"))

	select {
	case evt := <-ch:
		if evt.Type != TypeEntry {
			t.Errorf("evt.Type = %q, want %q", evt.Type, TypeEntry)
		}
		if evt.Severity != eventbus.SeverityWarning {
			t.Errorf("evt.Severity = %q, want warning for a WARN-level entry", evt.Severity)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for published event")
	}
}

func TestCaptureIsSafeForConcurrentUse(t *testing.T) {
	e := New(nil, 100)
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func(n int) {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 50; j++ {
				e.Capture(context.Background(), record(slog.LevelInfo, "concurrent"))
			}
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}
	got := e.Query(Query{Limit: 1000})
	if len(got) != 100 {
		t.Fatalf("len(got) = %d, want 100 (capacity-bounded even under concurrent writers)", len(got))
	}
}
