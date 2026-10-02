package ratelimit

import (
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/Vesperis-group/gophishfr/middleware/clientip"
	"golang.org/x/time/rate"
)

// DefaultFailureBurst is the maximum number of client-attributable
// authentication failures tolerated from a single client IP before it is
// blocked. It is intentionally larger than DefaultRequestsPerMinute (the
// /login limiter) because only failures ever consume this budget: a
// high-throughput, well-behaved API client spends nothing from it, and
// shared NAT/CI egress IPs are common for legitimate API traffic.
const DefaultFailureBurst = 20

// DefaultFailureRefillPerMinute is the rate, in recovered failures per
// minute, at which a client IP's failure budget refills once it is below
// DefaultFailureBurst.
const DefaultFailureRefillPerMinute = 20

// failureBucket tracks the remaining authentication-failure budget for one
// client IP. All access to limiter goes through mu, so the "peek" trick used
// by blocked/retryAfter below (reserve a token, then immediately cancel the
// reservation) can never interleave with a concurrent recordFailure call on
// the same bucket and transiently under-count or over-count the budget.
type failureBucket struct {
	mu       sync.Mutex
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newFailureBucket(burst int, refillPerMinute int) *failureBucket {
	return &failureBucket{
		limiter: rate.NewLimiter(rate.Every(time.Minute/time.Duration(refillPerMinute)), burst),
	}
}

// blocked reports whether the bucket's failure budget is currently
// exhausted, and if so, how long until one unit of budget would be
// available again. It never consumes or records anything: the underlying
// golang.org/x/time/rate.Reservation is canceled immediately, which (per
// that package's documented Cancel semantics) exactly restores the tokens
// it provisionally reserved, making this a true no-op peek.
func (b *failureBucket) blocked(now time.Time) (bool, time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	reservation := b.limiter.ReserveN(now, 1)
	delay := reservation.DelayFrom(now)
	reservation.CancelAt(now)
	return delay > 0, delay
}

// recordFailure consumes exactly one unit of failure budget, if any remains.
// If the budget is already exhausted, this is a harmless no-op: AllowN only
// commits a decrement when the result would be immediately available (see
// golang.org/x/time/rate), so the stored token count can never be driven
// negative by concurrent callers racing past an exhausted bucket.
func (b *failureBucket) recordFailure(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.limiter.AllowN(now, 1)
}

// FailureLimiter tracks per-client-IP authentication-failure budgets.
//
// Unlike PostLimiter (a classic "successful call consumes a token" limiter),
// FailureLimiter never consumes budget on success. Budget is consumed only
// by an explicit RecordFailure call, is never restored by a success, and
// only recovers through natural time-based refill. Blocked answers "is this
// IP currently blocked by its own prior failures" without recording
// anything, so callers can cheaply reject a request before doing any
// expensive work (HMAC/DB lookups) once an IP is blocked.
//
// Boundary convention (binding, see goal "Failure-budget semantics"): the
// request that consumes the last unit of budget (e.g. the 20th recorded
// failure against a fresh bucket) is NOT itself blocked -- Blocked is
// evaluated once, before that request's own authentication attempt, and at
// that point budget was still available. Only the NEXT request, whose
// Blocked pre-check now finds the budget at zero, is rejected with a 429.
type FailureLimiter struct {
	visitors        map[string]*failureBucket
	burst           int
	refillPerMinute int
	cleanupInterval time.Duration
	expiry          time.Duration
	clientIP        ClientIPFunc
	now             func() time.Time
	newTicker       func(time.Duration) *time.Ticker
	stop            chan struct{}
	stopOnce        sync.Once
	sync.RWMutex
}

// FailureLimiterOption is a functional option that configures a FailureLimiter.
type FailureLimiterOption func(*FailureLimiter)

// WithFailureBurst sets the maximum failure budget (burst size).
func WithFailureBurst(burst int) FailureLimiterOption {
	return func(l *FailureLimiter) {
		l.burst = burst
	}
}

// WithFailureRefillPerMinute sets the failure-budget refill rate, in
// recovered failures per minute.
func WithFailureRefillPerMinute(refillPerMinute int) FailureLimiterOption {
	return func(l *FailureLimiter) {
		l.refillPerMinute = refillPerMinute
	}
}

// WithFailureCleanupInterval sets the interval between periodic purges of
// inactive buckets.
func WithFailureCleanupInterval(interval time.Duration) FailureLimiterOption {
	return func(l *FailureLimiter) {
		l.cleanupInterval = interval
	}
}

// WithFailureExpiry sets how long an inactive bucket is retained before the
// periodic cleanup purges it.
func WithFailureExpiry(expiry time.Duration) FailureLimiterOption {
	return func(l *FailureLimiter) {
		l.expiry = expiry
	}
}

// WithFailureClientIP configures the function used to resolve the
// rate-limiting identity for each request. Production callers must supply a
// (*clientip.Resolver).ClientIP configured with their trusted proxies -- the
// same resolver instance used elsewhere in the request chain -- so there is
// exactly one source of truth for client IP. The default (when this option
// is not supplied) trusts no proxy at all and resolves to the direct socket
// peer only.
func WithFailureClientIP(fn ClientIPFunc) FailureLimiterOption {
	return func(l *FailureLimiter) {
		l.clientIP = fn
	}
}

// WithFailureClock overrides the time source used for budget accounting
// (Blocked/RecordFailure/RetryAfter). Tests use this to simulate refill and
// boundary behavior deterministically, without a real sleep. Production
// callers should never set this; it defaults to time.Now.
func WithFailureClock(now func() time.Time) FailureLimiterOption {
	return func(l *FailureLimiter) {
		l.now = now
	}
}

// NewFailureLimiter returns a new FailureLimiter with DefaultFailureBurst /
// DefaultFailureRefillPerMinute parameters, and starts its periodic cleanup
// goroutine. Callers that want a different, explicitly injected client IP
// resolver (as every production caller must) should pass
// WithFailureClientIP.
func NewFailureLimiter(opts ...FailureLimiterOption) *FailureLimiter {
	limiter := &FailureLimiter{
		visitors:        make(map[string]*failureBucket),
		burst:           DefaultFailureBurst,
		refillPerMinute: DefaultFailureRefillPerMinute,
		cleanupInterval: DefaultCleanupInterval,
		expiry:          DefaultExpiry,
		clientIP:        clientip.NewResolver(nil).ClientIP,
		now:             time.Now,
		newTicker:       time.NewTicker,
		stop:            make(chan struct{}),
	}
	for _, opt := range opts {
		opt(limiter)
	}
	go limiter.pollCleanup()
	return limiter
}

// Stop terminates the periodic cleanup goroutine started by
// NewFailureLimiter. Safe to call more than once; safe to omit entirely in
// production, exactly like (*PostLimiter).Stop.
func (l *FailureLimiter) Stop() {
	l.stopOnce.Do(func() {
		close(l.stop)
	})
}

func (l *FailureLimiter) pollCleanup() {
	ticker := l.newTicker(l.cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			l.Cleanup()
		case <-l.stop:
			return
		}
	}
}

// Cleanup removes any buckets that were last seen past the configured
// expiry.
func (l *FailureLimiter) Cleanup() {
	l.Lock()
	defer l.Unlock()
	for ip, bucket := range l.visitors {
		if time.Since(bucket.lastSeen) >= l.expiry {
			delete(l.visitors, ip)
		}
	}
}

// ResolveClientIP resolves the rate-limiting identity for r using the
// single configured client IP function. The new limiter never re-derives
// client IP any other way.
func (l *FailureLimiter) ResolveClientIP(r *http.Request) string {
	return l.clientIP(r)
}

func (l *FailureLimiter) bucketFor(ip string, now time.Time) *failureBucket {
	l.RLock()
	bucket, exists := l.visitors[ip]
	l.RUnlock()
	if !exists {
		l.Lock()
		bucket, exists = l.visitors[ip]
		if !exists {
			bucket = newFailureBucket(l.burst, l.refillPerMinute)
			l.visitors[ip] = bucket
		}
		l.Unlock()
	}
	l.Lock()
	bucket.lastSeen = now
	l.Unlock()
	return bucket
}

// Blocked reports whether ip is currently blocked by its own prior recorded
// failures. It never consumes or records a failure, and never requires a
// successful or failed authentication attempt to have just occurred: it is
// safe (and intended) to call before doing any other work.
func (l *FailureLimiter) Blocked(ip string) bool {
	now := l.now()
	blocked, _ := l.bucketFor(ip, now).blocked(now)
	return blocked
}

// RecordFailure records exactly one client-attributable authentication
// failure against ip. It must never be called for a successful
// authentication, a session-only request, an authorization (RBAC) failure,
// or a server-side/infrastructure failure.
func (l *FailureLimiter) RecordFailure(ip string) {
	now := l.now()
	l.bucketFor(ip, now).recordFailure(now)
}

// RetryAfter returns the non-negative duration until ip's failure budget
// will next allow an attempt. It returns 0 if ip is not currently blocked.
func (l *FailureLimiter) RetryAfter(ip string) time.Duration {
	now := l.now()
	_, delay := l.bucketFor(ip, now).blocked(now)
	if delay < 0 {
		return 0
	}
	return delay
}

// RetryAfterSeconds returns RetryAfter(ip) as the non-negative integer
// delta-seconds value required by the Retry-After response header: rounded
// up so a caller never under-promises the wait, with a minimum of 1 second
// whenever any positive wait is needed at all.
func (l *FailureLimiter) RetryAfterSeconds(ip string) int64 {
	delay := l.RetryAfter(ip)
	if delay <= 0 {
		return 0
	}
	seconds := int64(math.Ceil(delay.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	return seconds
}
