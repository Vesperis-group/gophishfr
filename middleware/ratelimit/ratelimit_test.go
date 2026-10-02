package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/Vesperis-group/gophishfr/middleware/clientip"
)

var successHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte("ok"))
})

func reachLimit(t *testing.T, handler http.Handler, limit int) {
	// Make `expected` requests and ensure that each return a successful
	// response.
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.RemoteAddr = "127.0.0.1:"
	for i := 0; i < limit; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("no 200 on req %d got %d", i, w.Code)
		}
	}
	// Then, makes another request to ensure it returns the 429
	// status.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("no 429")
	}
}

func TestRateLimitEnforcement(t *testing.T) {
	expectedLimit := 3
	limiter := NewPostLimiter(WithRequestsPerMinute(expectedLimit))
	defer limiter.Stop()
	handler := limiter.Limit(successHandler)
	reachLimit(t, handler, expectedLimit)
}

func TestRateLimitCleanup(t *testing.T) {
	expectedLimit := 3
	limiter := NewPostLimiter(WithRequestsPerMinute(expectedLimit))
	defer limiter.Stop()
	handler := limiter.Limit(successHandler)
	reachLimit(t, handler, expectedLimit)

	// Set the timeout to be
	bucket, exists := limiter.visitors["127.0.0.1"]
	if !exists {
		t.Fatalf("doesn't exist for some reason")
	}
	bucket.lastSeen = bucket.lastSeen.Add(-limiter.expiry)
	limiter.Cleanup()
	_, exists = limiter.visitors["127.0.0.1"]
	if exists {
		t.Fatalf("exists for some reason")
	}
	reachLimit(t, handler, expectedLimit)
}

// TestCleanupTickerUsesConfiguredIntervalDirectly proves the pollCleanup
// double-multiplication bug (cleanupInterval * time.Second, inflating a
// configured 1-minute interval to ~147.7 years) is fixed: the ticker must
// actually fire at the configured interval, not merely compute to the right
// duration on paper.
func TestCleanupTickerUsesConfiguredIntervalDirectly(t *testing.T) {
	const interval = 15 * time.Millisecond
	limiter := NewPostLimiter(WithCleanupInterval(interval), WithExpiry(1*time.Millisecond))
	defer limiter.Stop()

	limiter.addBucket("203.0.113.5")
	bucket, exists := limiter.visitors["203.0.113.5"]
	if !exists {
		t.Fatalf("bucket was not created")
	}
	// Already expired as of creation; only the periodic ticker (never
	// Cleanup() called directly) will remove it.
	bucket.lastSeen = time.Now().Add(-time.Hour)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		limiter.RLock()
		_, stillExists := limiter.visitors["203.0.113.5"]
		limiter.RUnlock()
		if !stillExists {
			return // purged by the periodic mechanism: bug is fixed.
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expired bucket was never purged by the periodic cleanup ticker within 2s "+
		"(with a %s configured interval); the ticker is not firing at the configured rate", interval)
}

// TestClientIPDefaultsToDirectPeer proves that, absent any WithClientIP
// option, the limiter still resolves to the real socket peer and ignores a
// forged X-Forwarded-For header (the pre-existing, pre-primitive raw
// net.SplitHostPort behavior, now backed by the safe default resolver).
func TestClientIPDefaultsToDirectPeer(t *testing.T) {
	limiter := NewPostLimiter(WithRequestsPerMinute(1))
	defer limiter.Stop()
	handler := limiter.Limit(successHandler)

	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.RemoteAddr = "203.0.113.9:4444"
	r.Header.Set("X-Forwarded-For", "9.9.9.9")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected first request to succeed, got %d", w.Code)
	}

	if _, exists := limiter.visitors["203.0.113.9"]; !exists {
		t.Fatalf("expected bucket keyed by the real socket peer, not the forged header")
	}
	if _, exists := limiter.visitors["9.9.9.9"]; exists {
		t.Fatalf("forged X-Forwarded-For must never create its own bucket")
	}
}

// TestLoginLimiterSpoofBypassProof is the central spoof-bypass proof
// required by the goal: behind the same simulated trusted proxy, two
// requests from the same real client with different forged leftmost XFF
// entries must resolve to the same client IP and consume the same
// rate-limit bucket (and therefore the same quota).
func TestLoginLimiterSpoofBypassProof(t *testing.T) {
	resolver := clientip.NewResolver(mustTrustedProxies(t, "127.0.0.1/32"))
	limiter := NewPostLimiter(WithRequestsPerMinute(1), WithClientIP(resolver.ClientIP))
	defer limiter.Stop()
	handler := limiter.Limit(successHandler)

	newRequest := func(forgedLeftmost string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.RemoteAddr = "127.0.0.1:12345" // the trusted nginx peer
		r.Header.Set("X-Forwarded-For", forgedLeftmost+", 203.0.113.42")
		return r
	}

	// First request (from the real client, nginx-proxied) consumes the
	// single allowed slot.
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, newRequest("1.1.1.1"))
	if w1.Code != http.StatusOK {
		t.Fatalf("expected first request to succeed, got %d", w1.Code)
	}

	// Second request, same real client, but with a *different* forged
	// leftmost XFF entry. If the spoof-bypass were possible, this would be
	// treated as a different bucket and also succeed.
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, newRequest("2.2.2.2"))
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected second request (same real client, different forged XFF) to be "+
			"rate-limited by the same bucket, got %d", w2.Code)
	}

	if len(limiter.visitors) != 1 {
		t.Fatalf("expected exactly one bucket (same real client IP), got %d: %v", len(limiter.visitors), limiter.visitors)
	}
	if _, exists := limiter.visitors["203.0.113.42"]; !exists {
		t.Fatalf("expected the bucket to be keyed by the real (rightmost, untrusted) client IP")
	}
}

// TestDirectAttackerCannotChangeBucketViaXFF proves a direct (non-proxied)
// attacker sending a forged X-Forwarded-For can never change which
// rate-limit bucket their own requests consume.
func TestDirectAttackerCannotChangeBucketViaXFF(t *testing.T) {
	limiter := NewPostLimiter(WithRequestsPerMinute(1)) // no trusted proxies configured
	defer limiter.Stop()
	handler := limiter.Limit(successHandler)

	newRequest := func(forged string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.RemoteAddr = "203.0.113.1:9999"
		r.Header.Set("X-Forwarded-For", forged)
		return r
	}

	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, newRequest("1.1.1.1"))
	if w1.Code != http.StatusOK {
		t.Fatalf("expected first request to succeed, got %d", w1.Code)
	}

	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, newRequest("2.2.2.2"))
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected the attacker's second request to hit the same bucket despite the "+
			"different forged X-Forwarded-For, got %d", w2.Code)
	}
}

func mustTrustedProxies(t *testing.T, entries ...string) []netip.Prefix {
	t.Helper()
	prefixes, err := clientip.ParseTrustedProxies(entries)
	if err != nil {
		t.Fatalf("invalid test trusted proxies %v: %v", entries, err)
	}
	return prefixes
}
