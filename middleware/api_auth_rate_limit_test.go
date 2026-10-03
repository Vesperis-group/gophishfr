package middleware

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	ctx "github.com/Vesperis-group/gophishfr/context"
	"github.com/Vesperis-group/gophishfr/middleware/clientip"
	"github.com/Vesperis-group/gophishfr/middleware/ratelimit"
	"github.com/Vesperis-group/gophishfr/models"
)

// withFreshAPIAuthLimiter installs a fresh FailureLimiter configured with
// the given options for the duration of one test, and restores a plain
// default limiter afterward (consistent with setupTest's own reset, so a
// later test in the same package is never affected by this one).
func withFreshAPIAuthLimiter(t *testing.T, opts ...ratelimit.FailureLimiterOption) *ratelimit.FailureLimiter {
	t.Helper()
	limiter := ratelimit.NewFailureLimiter(opts...)
	SetAPIAuthRateLimiter(limiter)
	t.Cleanup(func() {
		SetAPIAuthRateLimiter(ratelimit.NewFailureLimiter())
	})
	return limiter
}

func explicitCredentialRequest(remoteAddr, authorization string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	r.RemoteAddr = remoteAddr
	r.Header.Set("Authorization", authorization)
	return r
}

func remoteAddrHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

func mustRateLimitTestTrustedProxies(t *testing.T, entries ...string) []netip.Prefix {
	t.Helper()
	prefixes, err := clientip.ParseTrustedProxies(entries)
	if err != nil {
		t.Fatalf("invalid test trusted proxies %v: %v", entries, err)
	}
	return prefixes
}

// installCountingAPIKeyLookup replaces the package-level apiKeyLookup
// indirection with one that counts invocations, for the duration of the
// returned restore function's lifetime. This is the test-only
// instrumentation point proving HMAC/DB work (apiKeyLookup wraps
// models.GetUserByAPIKey, which performs both) is skipped once an IP is
// blocked: apiKeyLookup is unexported, so no production or externally
// reachable code path can retarget it.
func installCountingAPIKeyLookup(t *testing.T, calls *int) func() {
	t.Helper()
	original := apiKeyLookup
	apiKeyLookup = func(token string) (models.User, error) {
		*calls++
		return original(token)
	}
	return func() {
		apiKeyLookup = original
	}
}

func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

func decodeJSONResponse(body []byte, out *models.Response) error {
	return json.Unmarshal(body, out)
}

// TestAPIAuthRateLimitBelowThreshold proves N failures below the configured
// threshold (N < burst) each still receive their historical per-case 401,
// not a 429, uniformly for Bearer, raw Authorization, query, and form.
func TestAPIAuthRateLimitBelowThreshold(t *testing.T) {
	setupTest(t)
	withFreshAPIAuthLimiter(t, ratelimit.WithFailureBurst(20), ratelimit.WithFailureRefillPerMinute(20))

	transports := []func(remoteAddr string) *http.Request{
		func(remoteAddr string) *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/api/test", nil)
			r.RemoteAddr = remoteAddr
			r.Header.Set("Authorization", "Bearer bogus-bearer-token")
			return r
		},
		func(remoteAddr string) *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/api/test", nil)
			r.RemoteAddr = remoteAddr
			r.Header.Set("Authorization", "bogus-raw-token")
			return r
		},
		func(remoteAddr string) *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/api/test?api_key=bogus-query-token", nil)
			r.RemoteAddr = remoteAddr
			return r
		},
		func(remoteAddr string) *http.Request {
			r := httptest.NewRequest(http.MethodPost, "/api/test",
				strings.NewReader("api_key=bogus-form-token"))
			r.RemoteAddr = remoteAddr
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			return r
		},
	}

	for i, makeRequest := range transports {
		response := httptest.NewRecorder()
		RequireAPIKey(successHandler).ServeHTTP(response, makeRequest("198.51.100.1:1111"))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("transport %d: expected historical 401 below threshold, got %d", i, response.Code)
		}
	}
}

// TestAPIAuthRateLimitBoundary is the integration-level proof of the same
// boundary convention proven at the unit level in middleware/ratelimit: the
// 20th failing request for one IP still gets its historical 401; the 21st
// gets 429 with Retry-After, and never reaches apiKeyLookup (proving
// HMAC/DB work is skipped once blocked).
func TestAPIAuthRateLimitBoundary(t *testing.T) {
	setupTest(t)
	withFreshAPIAuthLimiter(t, ratelimit.WithFailureBurst(20), ratelimit.WithFailureRefillPerMinute(20))

	const remoteAddr = "198.51.100.2:2222"
	for i := 0; i < 20; i++ {
		response := httptest.NewRecorder()
		RequireAPIKey(successHandler).ServeHTTP(response, explicitCredentialRequest(remoteAddr, "bogus-boundary-token"))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d: expected historical 401, got %d", i+1, response.Code)
		}
	}

	lookupCalls := 0
	restore := installCountingAPIKeyLookup(t, &lookupCalls)
	defer restore()

	response := httptest.NewRecorder()
	RequireAPIKey(successHandler).ServeHTTP(response, explicitCredentialRequest(remoteAddr, "bogus-boundary-token"))
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("21st request: expected 429, got %d: %s", response.Code, response.Body.String())
	}
	if lookupCalls != 0 {
		t.Fatalf("21st request: expected HMAC/DB lookup to be skipped once blocked, but it was called %d time(s)", lookupCalls)
	}
	retryAfter := response.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatal("expected a Retry-After header on the 429 response")
	}
	seconds, err := strconv.Atoi(retryAfter)
	if err != nil {
		t.Fatalf("Retry-After must be an integer delta-seconds value, got %q: %v", retryAfter, err)
	}
	if seconds < 1 {
		t.Fatalf("Retry-After must be a positive integer, got %d", seconds)
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("429 response is not JSON: %q", response.Header().Get("Content-Type"))
	}
	var body models.Response
	if err := decodeJSONResponse(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode 429 body: %v", err)
	}
	if body.Success {
		t.Fatal("429 response must report success=false")
	}
	for _, leak := range []string{"token", "user", "locked", "verifier", "bearer", "boundary"} {
		if containsFold(body.Message, leak) {
			t.Fatalf("429 body leaks an oracle term %q: %q", leak, body.Message)
		}
	}
}

// TestAPIAuthRateLimitSkipsHMACAndDBWhenBlocked is the dedicated,
// instrumentation-based proof required by the goal: once an IP is blocked,
// apiKeyLookup (which performs HMAC candidate computation and the database
// lookup) must never be invoked, for any explicit credential -- including
// one that would otherwise have succeeded.
func TestAPIAuthRateLimitSkipsHMACAndDBWhenBlocked(t *testing.T) {
	testCtx := setupTest(t)
	withFreshAPIAuthLimiter(t, ratelimit.WithFailureBurst(1), ratelimit.WithFailureRefillPerMinute(20))

	const remoteAddr = "198.51.100.3:3333"
	// Exhaust the burst of 1.
	response := httptest.NewRecorder()
	RequireAPIKey(successHandler).ServeHTTP(response, explicitCredentialRequest(remoteAddr, "bogus-skip-token"))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected the first (budget-exhausting) failure to still get historical 401, got %d", response.Code)
	}

	lookupCalls := 0
	restore := installCountingAPIKeyLookup(t, &lookupCalls)
	defer restore()

	// Even a request carrying a genuinely valid API key must be blocked
	// without ever reaching the lookup.
	response = httptest.NewRecorder()
	RequireAPIKey(successHandler).ServeHTTP(response, explicitCredentialRequest(remoteAddr, testCtx.apiKey))
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for a blocked ip even with a valid key, got %d", response.Code)
	}
	if lookupCalls != 0 {
		t.Fatalf("expected apiKeyLookup never to be called once blocked, got %d call(s)", lookupCalls)
	}
}

// TestAPIAuthRateLimitBlockedIPRejectsRemovedTransportBeforeInspection
// proves the blocked-IP pre-check still runs first, exactly as for every
// other explicit-credential transport, for a request using a removed
// (query or form) api_key transport: a blocked IP gets 429 directly,
// without the removed transport's presence needing to be inspected at all
// and without needing a valid-looking token.
func TestAPIAuthRateLimitBlockedIPRejectsRemovedTransportBeforeInspection(t *testing.T) {
	setupTest(t)
	withFreshAPIAuthLimiter(t, ratelimit.WithFailureBurst(1), ratelimit.WithFailureRefillPerMinute(20))

	const remoteAddr = "198.51.100.86:8886"
	// Exhaust the burst of 1 using an ordinary bogus Authorization header.
	response := httptest.NewRecorder()
	RequireAPIKey(successHandler).ServeHTTP(response, explicitCredentialRequest(remoteAddr, "bogus-blocked-removed-transport-token"))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected the first (budget-exhausting) failure to still get historical 401, got %d", response.Code)
	}

	t.Run("query", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/api/test?api_key=anything-at-all", nil)
		r.RemoteAddr = remoteAddr
		response := httptest.NewRecorder()
		RequireAPIKey(successHandler).ServeHTTP(response, r)
		if response.Code != http.StatusTooManyRequests {
			t.Fatalf("expected 429 for a blocked ip with a removed query transport, got %d", response.Code)
		}
	})

	t.Run("form", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader("api_key=anything-at-all"))
		r.RemoteAddr = remoteAddr
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		RequireAPIKey(successHandler).ServeHTTP(response, r)
		if response.Code != http.StatusTooManyRequests {
			t.Fatalf("expected 429 for a blocked ip with a removed form transport, got %d", response.Code)
		}
	})
}

// TestAPIAuthRateLimitSuccessDoesNotConsumeOrResetBudget is the integration
// proof: valid-key successes neither consume nor reset the budget.
func TestAPIAuthRateLimitSuccessDoesNotConsumeOrResetBudget(t *testing.T) {
	testCtx := setupTest(t)
	withFreshAPIAuthLimiter(t, ratelimit.WithFailureBurst(20), ratelimit.WithFailureRefillPerMinute(20))

	const remoteAddr = "198.51.100.4:4444"
	for i := 0; i < 10; i++ {
		response := httptest.NewRecorder()
		RequireAPIKey(successHandler).ServeHTTP(response, explicitCredentialRequest(remoteAddr, "bogus-success-proof-token"))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d: expected 401, got %d", i+1, response.Code)
		}
	}

	// Many valid-key successes in between must spend and restore nothing.
	for i := 0; i < 50; i++ {
		response := httptest.NewRecorder()
		RequireAPIKey(successHandler).ServeHTTP(response, explicitCredentialRequest(remoteAddr, testCtx.apiKey))
		if response.Code != http.StatusOK {
			t.Fatalf("success %d: expected 200, got %d", i+1, response.Code)
		}
	}

	// Exactly 10 more failures (not 20) should now exhaust the budget,
	// proving the 50 successes neither consumed nor reset it.
	for i := 0; i < 10; i++ {
		response := httptest.NewRecorder()
		RequireAPIKey(successHandler).ServeHTTP(response, explicitCredentialRequest(remoteAddr, "bogus-success-proof-token"))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("remaining failure %d: expected historical 401, got %d", i+1, response.Code)
		}
	}
	response := httptest.NewRecorder()
	RequireAPIKey(successHandler).ServeHTTP(response, explicitCredentialRequest(remoteAddr, "bogus-success-proof-token"))
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after exactly 20 total failures with 50 intervening successes, got %d", response.Code)
	}
}

// TestAPIAuthRateLimitRBACAndViewOnlyDoNotConsumeBudget proves RBAC (403)
// and EnforceViewOnly refusals after a successful authentication never
// touch the limiter.
func TestAPIAuthRateLimitRBACAndViewOnlyDoNotConsumeBudget(t *testing.T) {
	setupTest(t)
	withFreshAPIAuthLimiter(t, ratelimit.WithFailureBurst(20), ratelimit.WithFailureRefillPerMinute(20))

	viewOnly := models.User{
		Username: "rate-limit-view-only-user",
		RoleID:   999999,
	}
	viewOnlyKey := createTestUserWithAPIKey(t, &viewOnly)

	const remoteAddr = "198.51.100.5:5555"
	for i := 0; i < 50; i++ {
		request := httptest.NewRequest(http.MethodPost, "/api/test", nil)
		request.RemoteAddr = remoteAddr
		request.Header.Set("Authorization", viewOnlyKey)
		response := httptest.NewRecorder()
		RequireAPIKey(EnforceViewOnly(successHandler)).ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("view-only attempt %d: expected 403, got %d", i+1, response.Code)
		}
	}
	for i := 0; i < 50; i++ {
		request := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		request.RemoteAddr = remoteAddr
		request.Header.Set("Authorization", viewOnlyKey)
		response := httptest.NewRecorder()
		RequireAPIKey(RequirePermission(models.PermissionModifySystem)(successHandler)).ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("RBAC attempt %d: expected 403, got %d", i+1, response.Code)
		}
	}

	// A full 20-failure budget must still be available afterward.
	for i := 0; i < 20; i++ {
		response := httptest.NewRecorder()
		RequireAPIKey(successHandler).ServeHTTP(response, explicitCredentialRequest(remoteAddr, "bogus-rbac-proof-token"))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d: blocked before the full budget (unconsumed by RBAC/view-only) was exhausted, got %d",
				i+1, response.Code)
		}
	}
	response := httptest.NewRecorder()
	RequireAPIKey(successHandler).ServeHTTP(response, explicitCredentialRequest(remoteAddr, "bogus-rbac-proof-token"))
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after 20 failures, got %d", response.Code)
	}
}

// TestAPIAuthRateLimitServerFailureDoesNotConsumeBudget proves a
// verifier-unavailable (server-side/infrastructure) failure never counts
// against the budget: a server-side outage must never be able to lock out
// every client IP.
func TestAPIAuthRateLimitServerFailureDoesNotConsumeBudget(t *testing.T) {
	setupTest(t)
	withFreshAPIAuthLimiter(t, ratelimit.WithFailureBurst(20), ratelimit.WithFailureRefillPerMinute(20))

	// Simulate the verifier keyring becoming unavailable.
	models.SetAPIKeyVerifier(nil)
	defer installTestAPIKeyVerifier(t)

	const remoteAddr = "198.51.100.6:6666"
	for i := 0; i < 50; i++ {
		response := httptest.NewRecorder()
		RequireAPIKey(successHandler).ServeHTTP(response, explicitCredentialRequest(remoteAddr, "any-token-during-outage"))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("outage attempt %d: expected 401, got %d", i+1, response.Code)
		}
	}
	if currentAPIAuthRateLimiter().Blocked(remoteAddrHost(remoteAddr)) {
		t.Fatalf("server-side verifier-unavailable failures must never block the ip")
	}
}

// TestAPIAuthRateLimitSessionBranchNeverTouchesLimiter proves zero limiter
// lookups/mutations on the pure-session branch: a session-only request to
// an IP that is already fully exhausted for explicit-credential attempts
// must still succeed, and an exhausted IP must never be produced by
// session-only traffic.
func TestAPIAuthRateLimitSessionBranchNeverTouchesLimiter(t *testing.T) {
	setupTest(t)
	withFreshAPIAuthLimiter(t, ratelimit.WithFailureBurst(20), ratelimit.WithFailureRefillPerMinute(20))

	sessionUser, err := models.GetUser(1)
	if err != nil {
		t.Fatal(err)
	}
	sessionUser.PasswordChangeRequired = false

	const remoteAddr = "198.51.100.7:7777"
	// A great many pure-session requests (no explicit credential at all)
	// must never be blocked and must never create/consume a bucket.
	for i := 0; i < 100; i++ {
		request := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		request.RemoteAddr = remoteAddr
		request = ctx.Set(request, "user", sessionUser)
		response := httptest.NewRecorder()
		RequireAPIKey(successHandler).ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("session request %d: expected 200, got %d", i+1, response.Code)
		}
	}

	if currentAPIAuthRateLimiter().Blocked(remoteAddrHost(remoteAddr)) {
		t.Fatalf("pure session-only traffic must never touch (or exhaust) the explicit-credential limiter")
	}

	// The same IP must still have its full, untouched explicit-credential
	// budget: exactly 20 explicit-credential failures are required to block
	// it next.
	for i := 0; i < 20; i++ {
		response := httptest.NewRecorder()
		RequireAPIKey(successHandler).ServeHTTP(response, explicitCredentialRequest(remoteAddr, "bogus-session-proof-token"))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("explicit failure %d: expected historical 401, got %d", i+1, response.Code)
		}
	}
	response := httptest.NewRecorder()
	RequireAPIKey(successHandler).ServeHTTP(response, explicitCredentialRequest(remoteAddr, "bogus-session-proof-token"))
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after 20 explicit-credential failures, got %d", response.Code)
	}
}

// TestAPIAuthRateLimitEmptyAndAmbiguousCountAsOneFailure proves empty
// explicit credentials count as a failure (not "no credential"), and
// ambiguous distinct credentials across transports in one request count as
// exactly one failure, not one per transport.
func TestAPIAuthRateLimitEmptyAndAmbiguousCountAsOneFailure(t *testing.T) {
	setupTest(t)
	withFreshAPIAuthLimiter(t, ratelimit.WithFailureBurst(20), ratelimit.WithFailureRefillPerMinute(20))

	t.Run("empty credential counts as a failure", func(t *testing.T) {
		const remoteAddr = "198.51.100.81:8881"
		for i := 0; i < 20; i++ {
			response := httptest.NewRecorder()
			RequireAPIKey(successHandler).ServeHTTP(response, explicitCredentialRequest(remoteAddr, ""))
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("empty-credential failure %d: expected 401, got %d", i+1, response.Code)
			}
		}
		response := httptest.NewRecorder()
		RequireAPIKey(successHandler).ServeHTTP(response, explicitCredentialRequest(remoteAddr, ""))
		if response.Code != http.StatusTooManyRequests {
			t.Fatalf("expected 429 after 20 empty-credential failures, got %d", response.Code)
		}
	})

	t.Run("ambiguous credentials count once per request, not once per transport", func(t *testing.T) {
		const remoteAddr = "198.51.100.82:8882"
		makeAmbiguous := func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/api/test?api_key=query-value", nil)
			r.RemoteAddr = remoteAddr
			r.Header.Set("Authorization", "header-value")
			return r
		}
		for i := 0; i < 19; i++ {
			response := httptest.NewRecorder()
			RequireAPIKey(successHandler).ServeHTTP(response, makeAmbiguous())
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("ambiguous failure %d: expected 401, got %d", i+1, response.Code)
			}
		}
		// This 20th ambiguous request carries TWO conflicting values (query +
		// header) in one request; it must still count as exactly one
		// failure, so it must still get the historical 401, not a 429.
		response := httptest.NewRecorder()
		RequireAPIKey(successHandler).ServeHTTP(response, makeAmbiguous())
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("20th ambiguous (multi-transport) request: expected historical 401, got %d", response.Code)
		}
		response = httptest.NewRecorder()
		RequireAPIKey(successHandler).ServeHTTP(response, makeAmbiguous())
		if response.Code != http.StatusTooManyRequests {
			t.Fatalf("expected 429 on the 21st ambiguous request, got %d", response.Code)
		}
	})
}

// TestAPIAuthRateLimitUnsupportedTransportCountsAsOneFailure proves the
// 0.13.0 removal of the query/form api_key transports (see
// docs/API_KEY_TRANSPORT_DEPRECATION.md) preserves the single-failure-per-
// request invariant: a removed transport's mere presence is one explicit
// API authentication failure, even when both removed transports are
// present simultaneously in the same request, and even when a
// simultaneously-present Authorization header would otherwise have been
// valid.
func TestAPIAuthRateLimitUnsupportedTransportCountsAsOneFailure(t *testing.T) {
	testCtx := setupTest(t)
	withFreshAPIAuthLimiter(t, ratelimit.WithFailureBurst(20), ratelimit.WithFailureRefillPerMinute(20))

	t.Run("query and form both present count once per request, not twice", func(t *testing.T) {
		const remoteAddr = "198.51.100.83:8883"
		makeCombined := func() *http.Request {
			r := httptest.NewRequest(
				http.MethodPost,
				"/api/test?api_key=query-removed-transport-value",
				strings.NewReader("api_key=form-removed-transport-value"),
			)
			r.RemoteAddr = remoteAddr
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			return r
		}
		for i := 0; i < 19; i++ {
			response := httptest.NewRecorder()
			RequireAPIKey(successHandler).ServeHTTP(response, makeCombined())
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("combined query+form failure %d: expected 401, got %d", i+1, response.Code)
			}
		}
		// This 20th request carries BOTH removed transports at once; it
		// must still count as exactly one failure, so it must still get
		// the historical 401, not a 429.
		response := httptest.NewRecorder()
		RequireAPIKey(successHandler).ServeHTTP(response, makeCombined())
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("20th combined query+form request: expected historical 401, got %d", response.Code)
		}
		response = httptest.NewRecorder()
		RequireAPIKey(successHandler).ServeHTTP(response, makeCombined())
		if response.Code != http.StatusTooManyRequests {
			t.Fatalf("expected 429 on the 21st combined query+form request, got %d", response.Code)
		}
	})

	t.Run("query present with a simultaneously valid authorization header counts once per request", func(t *testing.T) {
		const remoteAddr = "198.51.100.84:8884"
		makeRequest := func() *http.Request {
			r := httptest.NewRequest(
				http.MethodGet,
				"/api/test?api_key=unrelated-removed-transport-value",
				nil,
			)
			r.RemoteAddr = remoteAddr
			r.Header.Set("Authorization", testCtx.apiKey)
			return r
		}
		for i := 0; i < 19; i++ {
			response := httptest.NewRecorder()
			RequireAPIKey(successHandler).ServeHTTP(response, makeRequest())
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("failure %d: expected 401 (the valid Authorization value must not \"win\"), got %d", i+1, response.Code)
			}
		}
		response := httptest.NewRecorder()
		RequireAPIKey(successHandler).ServeHTTP(response, makeRequest())
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("20th request: expected historical 401, got %d", response.Code)
		}
		response = httptest.NewRecorder()
		RequireAPIKey(successHandler).ServeHTTP(response, makeRequest())
		if response.Code != http.StatusTooManyRequests {
			t.Fatalf("expected 429 on the 21st request, got %d", response.Code)
		}
	})

	t.Run("form present with a simultaneously valid authorization header counts once per request", func(t *testing.T) {
		const remoteAddr = "198.51.100.85:8885"
		makeRequest := func() *http.Request {
			r := httptest.NewRequest(
				http.MethodPost,
				"/api/test",
				strings.NewReader("api_key=unrelated-removed-transport-value"),
			)
			r.RemoteAddr = remoteAddr
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("Authorization", testCtx.apiKey)
			return r
		}
		for i := 0; i < 19; i++ {
			response := httptest.NewRecorder()
			RequireAPIKey(successHandler).ServeHTTP(response, makeRequest())
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("failure %d: expected 401 (the valid Authorization value must not \"win\"), got %d", i+1, response.Code)
			}
		}
		response := httptest.NewRecorder()
		RequireAPIKey(successHandler).ServeHTTP(response, makeRequest())
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("20th request: expected historical 401, got %d", response.Code)
		}
		response = httptest.NewRecorder()
		RequireAPIKey(successHandler).ServeHTTP(response, makeRequest())
		if response.Code != http.StatusTooManyRequests {
			t.Fatalf("expected 429 on the 21st request, got %d", response.Code)
		}
	})
}

// TestAPIAuthRateLimitUsesClientIPPrimitiveExclusively is the
// middleware-integration-level XFF-bypass proof: a direct (non-trusted)
// attacker's forged X-Forwarded-For must never rotate buckets through
// RequireAPIKey specifically (not just clientip in isolation), and the same
// real client behind a simulated trusted proxy must share one bucket
// despite different forged leftmost XFF entries.
func TestAPIAuthRateLimitUsesClientIPPrimitiveExclusively(t *testing.T) {
	setupTest(t)

	t.Run("direct attacker cannot rotate buckets via forged XFF", func(t *testing.T) {
		withFreshAPIAuthLimiter(t, ratelimit.WithFailureBurst(1), ratelimit.WithFailureRefillPerMinute(20))

		newRequest := func(forged string) *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/api/test", nil)
			r.RemoteAddr = "203.0.113.50:9999"
			r.Header.Set("Authorization", "bogus-xff-proof-token")
			r.Header.Set("X-Forwarded-For", forged)
			return r
		}

		response := httptest.NewRecorder()
		RequireAPIKey(successHandler).ServeHTTP(response, newRequest("1.1.1.1"))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("expected the first (budget-exhausting) failure to get 401, got %d", response.Code)
		}

		response = httptest.NewRecorder()
		RequireAPIKey(successHandler).ServeHTTP(response, newRequest("2.2.2.2"))
		if response.Code != http.StatusTooManyRequests {
			t.Fatalf("direct attacker rotated buckets via forged XFF (expected 429), got %d", response.Code)
		}
	})

	t.Run("same real client behind a trusted proxy shares one bucket despite different forged leftmost XFF", func(t *testing.T) {
		resolver := clientip.NewResolver(mustRateLimitTestTrustedProxies(t, "127.0.0.1/32"))
		withFreshAPIAuthLimiter(t, ratelimit.WithFailureBurst(1), ratelimit.WithFailureRefillPerMinute(20),
			ratelimit.WithFailureClientIP(resolver.ClientIP))

		newRequest := func(forgedLeftmost string) *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/api/test", nil)
			r.RemoteAddr = "127.0.0.1:12345" // the trusted nginx peer
			r.Header.Set("Authorization", "bogus-xff-proof-token")
			r.Header.Set("X-Forwarded-For", forgedLeftmost+", 203.0.113.52")
			return r
		}

		response := httptest.NewRecorder()
		RequireAPIKey(successHandler).ServeHTTP(response, newRequest("1.1.1.1"))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("expected the first (budget-exhausting) failure to get 401, got %d", response.Code)
		}

		response = httptest.NewRecorder()
		RequireAPIKey(successHandler).ServeHTTP(response, newRequest("2.2.2.2"))
		if response.Code != http.StatusTooManyRequests {
			t.Fatalf("same real client with a different forged leftmost XFF was not rate-limited by the same bucket, got %d",
				response.Code)
		}
	})
}

// TestAPIAuthRateLimitRefillsWithoutRealSleep is the middleware-integration
// refill proof, using an injected clock rather than a real sleep.
func TestAPIAuthRateLimitRefillsWithoutRealSleep(t *testing.T) {
	setupTest(t)
	clockValue := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return clockValue }
	withFreshAPIAuthLimiter(t, ratelimit.WithFailureBurst(1), ratelimit.WithFailureRefillPerMinute(20),
		ratelimit.WithFailureClock(clock))

	const remoteAddr = "198.51.100.9:9999"
	response := httptest.NewRecorder()
	RequireAPIKey(successHandler).ServeHTTP(response, explicitCredentialRequest(remoteAddr, "bogus-refill-token"))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected the budget-exhausting failure to get 401, got %d", response.Code)
	}

	response = httptest.NewRecorder()
	RequireAPIKey(successHandler).ServeHTTP(response, explicitCredentialRequest(remoteAddr, "bogus-refill-token"))
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 immediately after exhausting a burst of 1, got %d", response.Code)
	}

	// Advance the injected clock by one full refill interval (burst 1 at
	// 20/minute = one unit every 3 seconds) -- never a real sleep.
	clockValue = clockValue.Add(3 * time.Second)

	response = httptest.NewRecorder()
	RequireAPIKey(successHandler).ServeHTTP(response, explicitCredentialRequest(remoteAddr, "bogus-refill-token"))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected the ip to be unblocked after one refill interval, got %d", response.Code)
	}
}
