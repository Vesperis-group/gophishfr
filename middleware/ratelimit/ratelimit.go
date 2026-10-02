package ratelimit

import (
	"net/http"
	"sync"
	"time"

	log "github.com/Vesperis-group/gophishfr/logger"
	"github.com/Vesperis-group/gophishfr/middleware/clientip"
	"golang.org/x/time/rate"
)

// DefaultRequestsPerMinute is the number of requests to allow per minute.
// Any requests over this interval will return a HTTP 429 error.
const DefaultRequestsPerMinute = 5

// DefaultCleanupInterval determines how frequently the cleanup routine
// executes.
const DefaultCleanupInterval = 1 * time.Minute

// DefaultExpiry is the amount of time to track a bucket for a particular
// visitor.
const DefaultExpiry = 10 * time.Minute

type bucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// ClientIPFunc resolves the rate-limiting identity for a request. It must
// never trust client-controlled headers unless the immediate socket peer is
// an explicitly configured trusted proxy; see the middleware/clientip
// package, which is what every production caller supplies.
type ClientIPFunc func(*http.Request) string

// PostLimiter is a simple rate limiting middleware which only allows n POST
// requests per minute.
type PostLimiter struct {
	visitors        map[string]*bucket
	requestLimit    int
	cleanupInterval time.Duration
	expiry          time.Duration
	clientIP        ClientIPFunc
	newTicker       func(time.Duration) *time.Ticker
	stop            chan struct{}
	stopOnce        sync.Once
	sync.RWMutex
}

// PostLimiterOption is a functional option that allows callers to configure
// the rate limiter.
type PostLimiterOption func(*PostLimiter)

// WithRequestsPerMinute sets the number of requests to allow per minute.
func WithRequestsPerMinute(requestLimit int) PostLimiterOption {
	return func(p *PostLimiter) {
		p.requestLimit = requestLimit
	}
}

// WithCleanupInterval sets the interval between cleaning up stale entries in
// the rate limit client list
func WithCleanupInterval(interval time.Duration) PostLimiterOption {
	return func(p *PostLimiter) {
		p.cleanupInterval = interval
	}
}

// WithExpiry sets the amount of time to store client entries before they are
// considered stale.
func WithExpiry(expiry time.Duration) PostLimiterOption {
	return func(p *PostLimiter) {
		p.expiry = expiry
	}
}

// WithClientIP configures the function used to resolve the rate-limiting
// identity for each request. Callers behind a reverse proxy should supply a
// (*clientip.Resolver).ClientIP configured with their trusted proxies; the
// default (when this option is not supplied) trusts no proxy at all and
// resolves to the direct socket peer only.
func WithClientIP(fn ClientIPFunc) PostLimiterOption {
	return func(p *PostLimiter) {
		p.clientIP = fn
	}
}

// NewPostLimiter returns a new instance of a PostLimiter
func NewPostLimiter(opts ...PostLimiterOption) *PostLimiter {
	limiter := &PostLimiter{
		visitors:        make(map[string]*bucket),
		requestLimit:    DefaultRequestsPerMinute,
		cleanupInterval: DefaultCleanupInterval,
		expiry:          DefaultExpiry,
		clientIP:        clientip.NewResolver(nil).ClientIP,
		newTicker:       time.NewTicker,
		stop:            make(chan struct{}),
	}
	for _, opt := range opts {
		opt(limiter)
	}
	go limiter.pollCleanup()
	return limiter
}

// Stop terminates the periodic cleanup goroutine started by NewPostLimiter.
// It is safe to call more than once, and safe to omit entirely in
// production (the process lifetime bounds the goroutine); it exists mainly
// so tests that construct many limiters do not accumulate live tickers for
// the remainder of the test binary.
func (limiter *PostLimiter) Stop() {
	limiter.stopOnce.Do(func() {
		close(limiter.stop)
	})
}

func (limiter *PostLimiter) pollCleanup() {
	ticker := limiter.newTicker(limiter.cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			limiter.Cleanup()
		case <-limiter.stop:
			return
		}
	}
}

// Cleanup removes any buckets that were last seen past the configured expiry.
func (limiter *PostLimiter) Cleanup() {
	limiter.Lock()
	defer limiter.Unlock()
	for ip, bucket := range limiter.visitors {
		if time.Since(bucket.lastSeen) >= limiter.expiry {
			delete(limiter.visitors, ip)
		}
	}
}

func (limiter *PostLimiter) addBucket(ip string) *bucket {
	limiter.Lock()
	defer limiter.Unlock()
	limit := rate.NewLimiter(rate.Every(time.Minute/time.Duration(limiter.requestLimit)), limiter.requestLimit)
	b := &bucket{
		limiter: limit,
	}
	limiter.visitors[ip] = b
	return b
}

func (limiter *PostLimiter) allow(ip string) bool {
	// Check if we have a limiter already active for this clientIP
	limiter.RLock()
	bucket, exists := limiter.visitors[ip]
	limiter.RUnlock()
	if !exists {
		bucket = limiter.addBucket(ip)
	}
	// Update the lastSeen for this bucket to assist with cleanup
	limiter.Lock()
	defer limiter.Unlock()
	bucket.lastSeen = time.Now()
	return bucket.limiter.Allow()
}

// Limit enforces the configured rate limit for POST requests.
//
// TODO: Change the return value to an http.Handler when we clean up the
// way GophishFR routing is done.
func (limiter *PostLimiter) Limit(next http.Handler) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientIP := limiter.clientIP(r)
		if r.Method == http.MethodPost && !limiter.allow(clientIP) {
			log.Error("")
			http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
