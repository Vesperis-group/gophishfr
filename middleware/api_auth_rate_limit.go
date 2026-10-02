package middleware

import (
	"errors"
	"net/http"
	"strconv"
	"sync"

	"github.com/Vesperis-group/gophishfr/middleware/ratelimit"
	"github.com/Vesperis-group/gophishfr/models"
)

// apiAuthLimiter is the failure-budget rate limiter protecting explicit
// API-key authentication attempts (RequireAPIKey's explicit-credential
// branch only). It is never consulted on the pure-session branch.
//
// Parameters (burst/refill) are the v1 binding defaults from
// ratelimit.NewFailureLimiter and are not configurable in this release; see
// docs/API_AUTH_RATE_LIMITING.md. Production startup installs a client-IP
// resolver configured with the deployment's trusted proxies via
// SetAPIAuthRateLimiter; absent that call, the limiter still works safely,
// defaulting to trust-no-proxy client IP resolution exactly like
// ratelimit.NewPostLimiter's own default.
var (
	apiAuthLimiterMu sync.RWMutex
	apiAuthLimiter   *ratelimit.FailureLimiter
)

// apiKeyLookup is the indirection RequireAPIKey uses to look up a user by
// API credential. It defaults to models.GetUserByAPIKey and must never be
// reassigned in production code; it exists solely so tests in this package
// can prove that it is never invoked once a client IP is already blocked
// (the required pre-check-ordering proof), by substituting a
// call-counting/instrumented function for the duration of a single test.
// Because this variable is unexported, no externally reachable code path
// (production or otherwise) can retarget it; only test files compiled into
// this same package can.
var apiKeyLookup = models.GetUserByAPIKey

// currentAPIAuthRateLimiter returns the configured limiter, lazily
// constructing the safe-default instance (trust-no-proxy client IP
// resolution, v1 binding burst/refill parameters) on first use if
// SetAPIAuthRateLimiter was never called. This mirrors the lazy,
// explicitly-injectable-boundary pattern already used by
// models.SetAPIKeyVerifier/currentAPIKeyVerifier.
func currentAPIAuthRateLimiter() *ratelimit.FailureLimiter {
	apiAuthLimiterMu.RLock()
	limiter := apiAuthLimiter
	apiAuthLimiterMu.RUnlock()
	if limiter != nil {
		return limiter
	}
	apiAuthLimiterMu.Lock()
	defer apiAuthLimiterMu.Unlock()
	if apiAuthLimiter == nil {
		apiAuthLimiter = ratelimit.NewFailureLimiter()
	}
	return apiAuthLimiter
}

// SetAPIAuthRateLimiter installs the failure-budget rate limiter used by
// RequireAPIKey's explicit-credential branch. Production startup calls this
// once, with a limiter configured via ratelimit.WithFailureClientIP to reuse
// the deployment's single trusted-proxy-aware clientip.Resolver -- the same
// resolver instance already wired into the admin/phishing handler chains and
// reused by the /login PostLimiter. It is also an explicit injection
// boundary for isolated tests, which install a fresh instance to guarantee a
// full failure budget regardless of any other test's prior activity.
//
// The previous limiter's periodic cleanup goroutine, if any, is stopped
// before it is replaced.
func SetAPIAuthRateLimiter(limiter *ratelimit.FailureLimiter) {
	apiAuthLimiterMu.Lock()
	previous := apiAuthLimiter
	apiAuthLimiter = limiter
	apiAuthLimiterMu.Unlock()
	if previous != nil {
		previous.Stop()
	}
}

// isClientAttributableAPIAuthFailure reports whether err represents an
// authentication failure attributable to the client credential itself (and
// therefore must count against the caller's IP failure budget), as opposed
// to a server-side/infrastructure failure (verifier keyring unavailable,
// ambiguous internal state, a raw database error, or any other error) which
// must never count -- a server-side outage must never be able to lock out
// every client IP.
//
// This is deliberately the minimum possible classification: everything
// models.GetUserByAPIKey can return already collapses "unknown token" and
// "token belongs to a locked account" into the single
// models.ErrInvalidAPICredential sentinel (see models/user.go), so no new
// error type or oracle is introduced here -- only this one errors.Is check,
// which changes no externally-visible response for any request below the
// rate-limit threshold.
func isClientAttributableAPIAuthFailure(err error) bool {
	return errors.Is(err, models.ErrInvalidAPICredential)
}

// respondAPIAuthRateLimited writes the generic 429 response for a
// pre-check-blocked explicit API credential request. The body never reveals
// whether a token exists, whether a user exists, whether an account is
// locked, which verifier key matched, or which transport was used -- it is
// identical for every blocked request regardless of what the request would
// otherwise have been. Retry-After is always a non-negative integer
// delta-seconds value, never an HTTP-date, with a minimum of 1 whenever any
// positive wait is needed.
func respondAPIAuthRateLimited(w http.ResponseWriter, retryAfterSeconds int64) {
	w.Header().Set("Retry-After", strconv.FormatInt(retryAfterSeconds, 10))
	JSONError(w, http.StatusTooManyRequests, "Too Many Requests")
}
