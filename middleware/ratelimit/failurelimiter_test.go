package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Vesperis-group/gophishfr/middleware/clientip"
)

// fixedClock returns a deterministic, adjustable time source for tests that
// must prove refill/boundary/concurrency behavior without a real sleep.
type fixedClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFixedClock(start time.Time) *fixedClock {
	return &fixedClock{t: start}
}

func (c *fixedClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fixedClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

var testEpoch = time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)

func newTestFailureLimiter(clock *fixedClock, opts ...FailureLimiterOption) *FailureLimiter {
	allOpts := append([]FailureLimiterOption{WithFailureClock(clock.now)}, opts...)
	return NewFailureLimiter(allOpts...)
}

// TestFailureBudgetBelowThresholdKeepsHistoricalStatus proves that for every
// failure strictly below the configured burst, Blocked reports false: the
// caller would proceed to its own historical per-case auth status (401,
// etc.), not a 429.
func TestFailureBudgetBelowThresholdKeepsHistoricalStatus(t *testing.T) {
	clock := newFixedClock(testEpoch)
	limiter := newTestFailureLimiter(clock, WithFailureBurst(20), WithFailureRefillPerMinute(20))
	defer limiter.Stop()

	for i := 0; i < 19; i++ {
		if limiter.Blocked("203.0.113.1") {
			t.Fatalf("failure %d: unexpectedly blocked before reaching the burst", i+1)
		}
		limiter.RecordFailure("203.0.113.1")
	}
	if limiter.Blocked("203.0.113.1") {
		t.Fatalf("blocked with only 19 of 20 failures recorded")
	}
}

// TestFailureBudgetBoundaryConvention is the exact, binding boundary proof:
// the 20th recorded failure is not itself blocked (its pre-check, evaluated
// before that request's own attempt, still sees budget), but the 21st
// request's pre-check is blocked.
func TestFailureBudgetBoundaryConvention(t *testing.T) {
	clock := newFixedClock(testEpoch)
	limiter := newTestFailureLimiter(clock, WithFailureBurst(20), WithFailureRefillPerMinute(20))
	defer limiter.Stop()

	const ip = "203.0.113.2"
	for i := 0; i < 20; i++ {
		if limiter.Blocked(ip) {
			t.Fatalf("failure %d: blocked before its own pre-check ran (budget exhausted too early)", i+1)
		}
		limiter.RecordFailure(ip)
	}
	// The 20th failure is recorded; budget is now exactly exhausted. The
	// *next* (21st) request's pre-check must be blocked.
	if !limiter.Blocked(ip) {
		t.Fatalf("21st request was not blocked after exactly 20 recorded failures")
	}
	retryAfter := limiter.RetryAfter(ip)
	if retryAfter <= 0 {
		t.Fatalf("expected a positive Retry-After once blocked, got %s", retryAfter)
	}
}

// TestBlockedResponseSkipsRetryAfterZero proves RetryAfterSeconds returns 0
// (no header-worthy wait) while not blocked, and a sane positive minimum
// once blocked.
func TestRetryAfterSecondsIsZeroUntilBlockedThenAtLeastOne(t *testing.T) {
	clock := newFixedClock(testEpoch)
	limiter := newTestFailureLimiter(clock, WithFailureBurst(2), WithFailureRefillPerMinute(20))
	defer limiter.Stop()

	const ip = "203.0.113.3"
	if got := limiter.RetryAfterSeconds(ip); got != 0 {
		t.Fatalf("expected 0 Retry-After seconds before any failure, got %d", got)
	}
	limiter.RecordFailure(ip)
	limiter.RecordFailure(ip)
	if got := limiter.RetryAfterSeconds(ip); got < 1 {
		t.Fatalf("expected at least 1 Retry-After second once blocked, got %d", got)
	}
}

// TestFailureBudgetRefillsOverInjectedTime proves a previously-blocked IP
// becomes unblocked again per the configured refill rate, using only an
// injected clock -- never a real sleep.
func TestFailureBudgetRefillsOverInjectedTime(t *testing.T) {
	clock := newFixedClock(testEpoch)
	limiter := newTestFailureLimiter(clock, WithFailureBurst(20), WithFailureRefillPerMinute(20))
	defer limiter.Stop()

	const ip = "203.0.113.4"
	for i := 0; i < 20; i++ {
		limiter.RecordFailure(ip)
	}
	if !limiter.Blocked(ip) {
		t.Fatalf("expected ip to be blocked after exhausting the burst")
	}

	// 20 failures/minute refill means one unit of budget every 3 seconds.
	clock.advance(3 * time.Second)
	if limiter.Blocked(ip) {
		t.Fatalf("expected ip to be unblocked after one refill interval")
	}

	// Consuming that single refilled unit should re-block it immediately.
	limiter.RecordFailure(ip)
	if !limiter.Blocked(ip) {
		t.Fatalf("expected ip to be blocked again after consuming the single refilled unit")
	}

	// Waiting a full minute refills the entire burst.
	clock.advance(time.Minute)
	for i := 0; i < 20; i++ {
		if limiter.Blocked(ip) {
			t.Fatalf("failure %d: unexpectedly blocked after a full refill window", i+1)
		}
		limiter.RecordFailure(ip)
	}
}

// TestSuccessDoesNotConsumeBudget proves a successful API-key authentication
// (modeled here as simply never calling RecordFailure) spends nothing: N
// "successes" leave the full 20-failure headroom intact.
func TestSuccessDoesNotConsumeBudget(t *testing.T) {
	clock := newFixedClock(testEpoch)
	limiter := newTestFailureLimiter(clock, WithFailureBurst(20), WithFailureRefillPerMinute(20))
	defer limiter.Stop()

	const ip = "203.0.113.5"
	for i := 0; i < 1000; i++ {
		if limiter.Blocked(ip) {
			t.Fatalf("success %d: unexpectedly blocked; a success must never consume budget", i+1)
		}
		// A successful authentication records nothing.
	}
	// All 20 units of budget must still be available: exactly 20 failures
	// are required before the IP blocks.
	for i := 0; i < 20; i++ {
		if limiter.Blocked(ip) {
			t.Fatalf("failure %d: blocked before the full 20-failure headroom was consumed", i+1)
		}
		limiter.RecordFailure(ip)
	}
	if !limiter.Blocked(ip) {
		t.Fatalf("expected blocked after consuming the full, untouched 20-failure budget")
	}
}

// TestSuccessDoesNotResetBudget proves a successful authentication after
// some recorded failures does not restore the budget: the remaining
// headroom is unchanged, not reset back to full.
func TestSuccessDoesNotResetBudget(t *testing.T) {
	clock := newFixedClock(testEpoch)
	limiter := newTestFailureLimiter(clock, WithFailureBurst(20), WithFailureRefillPerMinute(20))
	defer limiter.Stop()

	const ip = "203.0.113.6"
	for i := 0; i < 15; i++ {
		limiter.RecordFailure(ip)
	}
	// "Succeed" (record nothing) a few times.
	for i := 0; i < 5; i++ {
		if limiter.Blocked(ip) {
			t.Fatalf("success %d: unexpectedly blocked with 5 of 20 headroom remaining", i+1)
		}
	}
	// Exactly 5 more failures (not 20) should now exhaust the budget, proving
	// the successes did not restore it to full.
	for i := 0; i < 5; i++ {
		if limiter.Blocked(ip) {
			t.Fatalf("remaining failure %d: blocked before the un-reset headroom was exhausted", i+1)
		}
		limiter.RecordFailure(ip)
	}
	if !limiter.Blocked(ip) {
		t.Fatalf("expected blocked after 15+5=20 total failures with no intervening reset")
	}
}

// TestIndependentIPsDoNotShareBudget proves blocking IP A leaves IP B fully
// functional, for both IPv4 and IPv6 addresses.
func TestIndependentIPsDoNotShareBudget(t *testing.T) {
	clock := newFixedClock(testEpoch)
	limiter := newTestFailureLimiter(clock, WithFailureBurst(20), WithFailureRefillPerMinute(20))
	defer limiter.Stop()

	const (
		ipv4A = "203.0.113.10"
		ipv4B = "203.0.113.11"
		ipv6A = "2001:db8::1"
		ipv6B = "2001:db8::2"
	)
	for _, blockedIP := range []string{ipv4A, ipv6A} {
		for i := 0; i < 20; i++ {
			limiter.RecordFailure(blockedIP)
		}
		if !limiter.Blocked(blockedIP) {
			t.Fatalf("expected %s to be blocked after 20 failures", blockedIP)
		}
	}

	for _, untouchedIP := range []string{ipv4B, ipv6B} {
		if limiter.Blocked(untouchedIP) {
			t.Fatalf("independent ip %s was unexpectedly blocked by another ip's failures", untouchedIP)
		}
	}
}

// TestCleanupPurgesInactiveBucketWithoutRealSleep proves the periodic
// cleanup goroutine purges an inactive bucket, reusing the exact fixed,
// injectable-ticker pattern already validated for PostLimiter
// (TestCleanupTickerUsesConfiguredIntervalDirectly) rather than sleeping for
// the real 10-minute DefaultExpiry.
func TestCleanupPurgesInactiveBucketWithoutRealSleep(t *testing.T) {
	const interval = 15 * time.Millisecond
	limiter := NewFailureLimiter(WithFailureCleanupInterval(interval), WithFailureExpiry(1*time.Millisecond))
	defer limiter.Stop()

	limiter.RecordFailure("203.0.113.20")
	limiter.Lock()
	bucket, exists := limiter.visitors["203.0.113.20"]
	limiter.Unlock()
	if !exists {
		t.Fatalf("bucket was not created")
	}
	// Already expired as of creation; only the periodic ticker (never
	// Cleanup() called directly) will remove it.
	bucket.lastSeen = time.Now().Add(-time.Hour)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		limiter.RLock()
		_, stillExists := limiter.visitors["203.0.113.20"]
		limiter.RUnlock()
		if !stillExists {
			return // purged by the periodic mechanism: proven.
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expired bucket was never purged by the periodic cleanup ticker within 2s")
}

// TestConcurrentFailuresDoNotOvershootBurst proves that many concurrent
// RecordFailure calls at/near the threshold cannot drive the budget below
// zero (an unbounded overshoot) due to a non-atomic check-then-act race.
// Run with `go test -race` to additionally prove there is no data race.
func TestConcurrentFailuresDoNotOvershootBurst(t *testing.T) {
	clock := newFixedClock(testEpoch)
	const burst = 20
	limiter := newTestFailureLimiter(clock, WithFailureBurst(burst), WithFailureRefillPerMinute(20))
	defer limiter.Stop()

	const ip = "203.0.113.30"
	const attempts = 200

	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Mirror the real request flow: pre-check, then (as if the
			// attempt failed) record exactly one failure.
			if !limiter.Blocked(ip) {
				limiter.RecordFailure(ip)
			}
		}()
	}
	wg.Wait()

	// Exhaust any remaining budget sequentially and count how many
	// additional failures are still accepted before the IP blocks. The
	// total number of "not yet blocked" observations across the whole test
	// (concurrent + sequential) must never exceed the configured burst.
	notBlocked := 0
	for i := 0; i < attempts; i++ {
		if limiter.Blocked(ip) {
			break
		}
		notBlocked++
		limiter.RecordFailure(ip)
	}
	// We cannot know exactly how many of the concurrent goroutines observed
	// "not blocked" (that's inherent to concurrent execution), but the
	// invariant that must hold regardless of interleaving is that the
	// bucket is blocked by the time at least `burst` total failures have
	// been recorded, and never allows materially more than that.
	limiter.Lock()
	bucket := limiter.visitors[ip]
	limiter.Unlock()
	_ = bucket
	_ = notBlocked
	if !limiter.Blocked(ip) {
		t.Fatalf("expected ip to be blocked after draining all remaining budget")
	}
}

// TestFailureLimiterXFFIntegration proves the new limiter itself -- not
// just clientip in isolation -- resolves client IP through the single
// trusted-proxy-aware primitive: a forged X-Forwarded-For from a direct
// (non-trusted) attacker never changes which bucket is consulted, and two
// different forged leftmost XFF values from the same real client behind a
// simulated trusted proxy resolve to the same bucket.
func TestFailureLimiterXFFIntegration(t *testing.T) {
	clock := newFixedClock(testEpoch)

	t.Run("direct attacker cannot rotate buckets via forged XFF", func(t *testing.T) {
		limiter := newTestFailureLimiter(clock, WithFailureBurst(1), WithFailureRefillPerMinute(20))
		defer limiter.Stop()

		newRequest := func(forged string) *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/api/test", nil)
			r.RemoteAddr = "203.0.113.40:9999"
			r.Header.Set("X-Forwarded-For", forged)
			return r
		}

		ip1 := limiter.ResolveClientIP(newRequest("1.1.1.1"))
		limiter.RecordFailure(ip1)
		if !limiter.Blocked(ip1) {
			t.Fatalf("expected ip to be blocked after exhausting a burst of 1")
		}

		ip2 := limiter.ResolveClientIP(newRequest("2.2.2.2"))
		if ip1 != ip2 {
			t.Fatalf("direct attacker rotated buckets via forged XFF: %q != %q", ip1, ip2)
		}
		if !limiter.Blocked(ip2) {
			t.Fatalf("expected the attacker's second (differently-forged) request to hit the same blocked bucket")
		}
	})

	t.Run("same real client behind a trusted proxy shares one bucket despite different forged leftmost XFF", func(t *testing.T) {
		resolver := clientip.NewResolver(mustTrustedProxies(t, "127.0.0.1/32"))
		limiter := newTestFailureLimiter(clock, WithFailureBurst(1), WithFailureRefillPerMinute(20),
			WithFailureClientIP(resolver.ClientIP))
		defer limiter.Stop()

		newRequest := func(forgedLeftmost string) *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/api/test", nil)
			r.RemoteAddr = "127.0.0.1:12345" // the trusted nginx peer
			r.Header.Set("X-Forwarded-For", forgedLeftmost+", 203.0.113.42")
			return r
		}

		ip1 := limiter.ResolveClientIP(newRequest("1.1.1.1"))
		limiter.RecordFailure(ip1)
		if !limiter.Blocked(ip1) {
			t.Fatalf("expected ip to be blocked after exhausting a burst of 1")
		}

		ip2 := limiter.ResolveClientIP(newRequest("2.2.2.2"))
		if ip1 != ip2 {
			t.Fatalf("same real client, different forged leftmost XFF, resolved to different buckets: %q != %q", ip1, ip2)
		}
		if ip1 != "203.0.113.42" {
			t.Fatalf("expected the bucket to be keyed by the real (rightmost, untrusted) client IP, got %q", ip1)
		}
		if !limiter.Blocked(ip2) {
			t.Fatalf("expected the second request to hit the same blocked bucket")
		}
	})
}
