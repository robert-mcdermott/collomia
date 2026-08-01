package agent

import (
	"sync"
	"time"
)

// Measuring whether a longer prompt-cache lifetime would pay for itself.
//
// The prompt-cache wave took the five-minute lifetime deliberately: the
// one-hour extension is requested through a beta header and is billed at a 2x
// write premium rather than 1.25x, and nothing had measured whether a session
// actually goes quiet long enough to need it. The live run that confirmed
// caching works measured back-to-back requests, which is the case the five
// minute lifetime already covers, so it could not answer the question.
//
// What decides it is the gap between one provider request and the next, because
// Anthropic refreshes the five-minute lifetime on every read: a chain of
// requests less than five minutes apart stays warm forever, and only a longer
// pause loses the prefix. So the useful count is not "how long are the gaps" but
// how many of them land in the window a longer lifetime would have rescued —
// over five minutes, and under an hour. A gap beyond an hour is cold under
// either setting and is evidence for neither.
const (
	shortCacheTTL = 5 * time.Minute
	longCacheTTL  = time.Hour
)

// cacheGapStats accumulates the inter-request gaps of one session.
type cacheGapStats struct {
	mu sync.Mutex
	// now is injectable so the tests do not sleep.
	now func() time.Time
	// last is when the previous provider request was issued.
	last time.Time
	// gaps counts intervals between consecutive requests. It is one less than
	// the number of requests: the first request has nothing to follow.
	gaps int
	// recoverable counts gaps longer than the five-minute lifetime and no
	// longer than an hour — the ones, and the only ones, a one-hour lifetime
	// would have turned from a cache write back into a cache read.
	recoverable int
	// coldEither counts gaps beyond an hour, which neither setting survives.
	coldEither int
	longest    time.Duration
}

// CacheGaps is the reportable form of the measurement.
type CacheGaps struct {
	Gaps        int
	Recoverable int
	ColdEither  int
	Longest     time.Duration
}

func newCacheGapStats() *cacheGapStats {
	return &cacheGapStats{now: time.Now}
}

// observeRequest records that a provider request is being issued now.
func (s *cacheGapStats) observeRequest() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	at := s.now()
	if !s.last.IsZero() {
		gap := at.Sub(s.last)
		// A clock that went backwards (suspend, NTP correction) says nothing
		// about cadence, and counting it would understate every real gap.
		if gap >= 0 {
			s.gaps++
			switch {
			case gap > longCacheTTL:
				s.coldEither++
			case gap > shortCacheTTL:
				s.recoverable++
			}
			if gap > s.longest {
				s.longest = gap
			}
		}
	}
	s.last = at
}

func (s *cacheGapStats) snapshot() CacheGaps {
	if s == nil {
		return CacheGaps{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return CacheGaps{Gaps: s.gaps, Recoverable: s.recoverable, ColdEither: s.coldEither, Longest: s.longest}
}
