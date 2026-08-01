package agent

import (
	"testing"
	"time"
)

// clockAt returns a stats accumulator driven by a caller-advanced clock, so
// the boundaries can be tested exactly rather than approximately.
func clockAt(start time.Time) (*cacheGapStats, func(time.Duration)) {
	now := start
	stats := &cacheGapStats{now: func() time.Time { return now }}
	return stats, func(d time.Duration) { now = now.Add(d) }
}

// The whole point of the measurement is which side of the five-minute
// lifetime a gap falls on, so the boundary is the test.
func TestOnlyGapsBeyondTheShortLifetimeCount(t *testing.T) {
	for _, tc := range []struct {
		name              string
		gap               time.Duration
		recoverable, cold int
	}{
		{"back to back", time.Second, 0, 0},
		{"just inside the short lifetime", shortCacheTTL - time.Second, 0, 0},
		{"exactly the short lifetime", shortCacheTTL, 0, 0},
		{"just past the short lifetime", shortCacheTTL + time.Second, 1, 0},
		{"well inside the long lifetime", 30 * time.Minute, 1, 0},
		{"exactly the long lifetime", longCacheTTL, 1, 0},
		{"past the long lifetime", longCacheTTL + time.Second, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stats, advance := clockAt(time.Now())
			stats.observeRequest()
			advance(tc.gap)
			stats.observeRequest()
			got := stats.snapshot()
			if got.Gaps != 1 {
				t.Fatalf("gaps = %d, want 1", got.Gaps)
			}
			if got.Recoverable != tc.recoverable {
				t.Errorf("recoverable = %d, want %d", got.Recoverable, tc.recoverable)
			}
			if got.ColdEither != tc.cold {
				t.Errorf("cold either way = %d, want %d", got.ColdEither, tc.cold)
			}
			if got.Longest != tc.gap {
				t.Errorf("longest = %s, want %s", got.Longest, tc.gap)
			}
		})
	}
}

// A single request has no gap to report. Counting one would make every session
// look like it paused once.
func TestFirstRequestProducesNoGap(t *testing.T) {
	stats, _ := clockAt(time.Now())
	stats.observeRequest()
	if got := stats.snapshot(); got.Gaps != 0 {
		t.Fatalf("gaps after one request = %d, want 0", got.Gaps)
	}
}

// A five-minute lifetime refreshes on every read, so a chain of short gaps
// stays warm however long the session runs. Reporting the elapsed session time
// instead would say the opposite.
func TestChainOfShortGapsNeverGoesCold(t *testing.T) {
	stats, advance := clockAt(time.Now())
	for i := 0; i < 60; i++ {
		stats.observeRequest()
		advance(4 * time.Minute)
	}
	got := stats.snapshot()
	if got.Gaps != 59 {
		t.Fatalf("gaps = %d, want 59", got.Gaps)
	}
	if got.Recoverable != 0 || got.ColdEither != 0 {
		t.Fatalf("a four-hour session of four-minute gaps reported %d recoverable and %d cold, want none",
			got.Recoverable, got.ColdEither)
	}
}

// Suspend and NTP correction both move the clock backwards. A negative gap is
// not evidence of a fast session and must not be recorded as one.
func TestBackwardClockIsNotCountedAsAGap(t *testing.T) {
	stats, advance := clockAt(time.Now())
	stats.observeRequest()
	advance(-time.Hour)
	stats.observeRequest()
	if got := stats.snapshot(); got.Gaps != 0 {
		t.Fatalf("a backwards clock recorded %d gaps, want 0", got.Gaps)
	}
}

func TestNilStatsAreSafe(t *testing.T) {
	var stats *cacheGapStats
	stats.observeRequest()
	if got := stats.snapshot(); got != (CacheGaps{}) {
		t.Fatalf("nil stats returned %+v", got)
	}
}

// Every Agent construction site has to measure, including delegated children.
func TestAgentReportsCacheGapsWithoutExplicitConstruction(t *testing.T) {
	a := &Agent{}
	a.cacheGapStatsOrInit().observeRequest()
	a.cacheGapStatsOrInit().observeRequest()
	if got := a.CacheGaps(); got.Gaps != 1 {
		t.Fatalf("gaps = %d, want 1", got.Gaps)
	}
}
