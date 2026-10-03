package middleware

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	csrf "filippo.io/csrf/gorilla"
	ctx "github.com/Vesperis-group/gophishfr/context"
	"github.com/Vesperis-group/gophishfr/models"
)

// explicitAPICredential is the mechanism-selection result shared by API
// authentication and CSRF handling. Values must never be included in errors or
// logs.
type explicitAPICredential struct {
	present   bool
	value     string
	ambiguous bool
	malformed bool
	// unsupportedTransport is true when a query or form `api_key` was
	// present (by key, regardless of value), per the 0.13.0 removal of
	// those transports (see docs/API_KEY_TRANSPORT_DEPRECATION.md). Their
	// mere presence is still detected -- so a request carrying one can
	// never silently fall back to session authentication, and so
	// CSRFExceptions' presence-based exemption still applies -- but their
	// values are no longer folded into the Bearer/raw credential value set
	// used for lookup/ambiguity.
	unsupportedTransport bool
}

const apiFormParseErrorContextKey = "api_form_parse_error"

type apiAuthenticationMechanism uint8

const (
	apiAuthenticationSession apiAuthenticationMechanism = iota + 1
	apiAuthenticationKey
	apiAuthenticationMechanismContextKey = "api_authentication_mechanism"
)

// IsSessionAuthentication reports whether RequireAPIKey selected ambient
// session authentication for this request. The marker contains no credential
// material and is set only after successful authentication.
func IsSessionAuthentication(r *http.Request) bool {
	mechanism, ok := ctx.Get(r, apiAuthenticationMechanismContextKey).(apiAuthenticationMechanism)
	return ok && mechanism == apiAuthenticationSession
}

// extractExplicitAPICredential collects every API credential transport.
// Presence is deliberately independent from value: an empty Authorization
// header or api_key parameter is still an explicit API-key authentication
// attempt. Distinct values are ambiguous; identical duplicates are accepted.
//
// The query and form `api_key` transports were removed for 0.13.0 (see
// docs/API_KEY_TRANSPORT_DEPRECATION.md): their presence is still detected
// -- via unsupportedTransport -- so a request carrying one is never
// silently treated as having no explicit credential at all, but their
// values are no longer folded into values, the Bearer/raw credential set
// used for lookup/ambiguity. RequireAPIKey rejects any unsupportedTransport
// request through its existing single malformed/ambiguous/empty rejection
// path, uniformly with every other kind of invalid explicit credential.
func extractExplicitAPICredential(r *http.Request) explicitAPICredential {
	// ParseForm is cached by net/http. GetContext normally called it already,
	// while this call keeps the extractor correct in focused middleware tests.
	parseErr := r.ParseForm()

	_, queryPresent := r.URL.Query()["api_key"]
	_, formPresent := r.PostForm["api_key"]
	unsupportedTransport := queryPresent || formPresent

	values := make([]string, 0)
	for _, authorization := range r.Header.Values("Authorization") {
		values = append(values, strings.TrimPrefix(authorization, "Bearer "))
	}

	malformed := parseErr != nil || ctx.Get(r, apiFormParseErrorContextKey) != nil
	if len(values) == 0 && !malformed && !unsupportedTransport {
		return explicitAPICredential{}
	}
	result := explicitAPICredential{
		present:              true,
		malformed:            malformed,
		unsupportedTransport: unsupportedTransport,
	}
	if len(values) == 0 {
		return result
	}
	result.value = values[0]
	for _, value := range values[1:] {
		if value != result.value {
			result.ambiguous = true
			break
		}
	}
	return result
}

// CSRFExceptions keeps explicit API-key clients exempt from browser same-origin
// checks. Session-authorized API requests are intentionally not exempt: unsafe
// methods are protected by the existing CrossOriginProtection engine.
func CSRFExceptions(handler http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api") && extractExplicitAPICredential(r).present {
			r = csrf.UnsafeSkipCheck(r)
		}
		handler.ServeHTTP(w, r)
	}
}

// CSRFFailureHandler preserves JSON error responses for protected API requests
// while retaining the existing error shape for browser page requests.
var CSRFFailureHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api") {
		JSONError(w, http.StatusForbidden, http.StatusText(http.StatusForbidden))
		return
	}
	http.Error(w, fmt.Sprintf("%s - %s",
		http.StatusText(http.StatusForbidden), csrf.FailureReason(r)),
		http.StatusForbidden)
})

// PlaintextHTTP is intentionally absent: filippo.io/csrf/gorilla derives the
// origin from Fetch metadata headers and ignores the plaintext-HTTP hint that
// github.com/gorilla/csrf v1.7.3 required. Keeping the middleware would have
// left code that reads as if it configured something.

// Use allows us to stack middleware to process the request
// Example taken from https://github.com/gorilla/mux/pull/36#issuecomment-25849172
func Use(handler http.HandlerFunc, mid ...func(http.Handler) http.HandlerFunc) http.HandlerFunc {
	for _, m := range mid {
		handler = m(handler)
	}
	return handler
}

// GetContext wraps each request in a function which fills in the context for a given request.
// This includes setting the User and Session keys and values as necessary for use in later functions.
func GetContext(handler http.Handler) http.HandlerFunc {
	// Set the context here
	return func(w http.ResponseWriter, r *http.Request) {
		// Parse the request form
		err := r.ParseForm()
		if err != nil {
			if strings.HasPrefix(r.URL.Path, "/api") {
				// Preserve the failure for the shared credential extractor. A
				// malformed encoded credential must never disappear and allow
				// fallback to ambient session authority.
				r = ctx.Set(r, apiFormParseErrorContextKey, true)
			} else {
				http.Error(w, "Error parsing request", http.StatusInternalServerError)
			}
		}
		// Set the context appropriately here.
		// Set the session
		session, _ := Store.Get(r, "gophish")
		// Put the session in the context so that we can
		// reuse the values in different handlers
		r = ctx.Set(r, "session", session)
		if id, ok := session.Values["id"].(int64); ok {
			u, err := models.GetUser(id)
			if err != nil {
				r = ctx.Set(r, "user", nil)
			} else {
				r = ctx.Set(r, "user", u)
			}
		} else {
			r = ctx.Set(r, "user", nil)
		}
		handler.ServeHTTP(w, r)
		// Remove context contents
		ctx.Clear(r)
	}
}

// RequireAPIKey selects and enforces one API authentication mechanism. Any
// explicit legacy API credential uses API-key authentication only; only a
// request with no explicit credential may use the session user populated by
// GetContext. API-key validation never falls back to ambient session authority.
func RequireAPIKey(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method == "OPTIONS" {
			w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
			w.Header().Set("Access-Control-Max-Age", "1000")
			w.Header().Set("Access-Control-Allow-Headers", "Origin, X-Requested-With, Content-Type, Accept")
			return
		}

		credential := extractExplicitAPICredential(r)
		if !credential.present {
			sessionUser := ctx.Get(r, "user")
			if sessionUser == nil {
				JSONError(w, http.StatusUnauthorized, "API Key not set")
				return
			}
			user, ok := sessionUser.(models.User)
			if !ok {
				JSONError(w, http.StatusUnauthorized, "Invalid session")
				return
			}
			if user.PasswordChangeRequired {
				JSONError(w, http.StatusForbidden, "Password change required")
				return
			}
			r = ctx.Set(r, apiAuthenticationMechanismContextKey, apiAuthenticationSession)
			r = ctx.Set(r, "user_id", user.Id)
			handler.ServeHTTP(w, r)
			return
		}

		// Every explicit-credential request (Bearer, raw Authorization, query,
		// or form -- uniformly, with no per-transport branching) is subject to
		// the failure-budget rate limiter before anything else is done with
		// it. If the resolved client IP is already blocked by its own prior
		// failures, HMAC candidate computation and the database lookup are
		// never performed, regardless of whether this particular request's
		// credential would otherwise have been malformed, ambiguous, empty,
		// unknown, or valid: the response must never become an oracle for any
		// of that. See docs/API_AUTH_RATE_LIMITING.md.
		limiter := currentAPIAuthRateLimiter()
		clientIP := limiter.ResolveClientIP(r)
		if limiter.Blocked(clientIP) {
			respondAPIAuthRateLimited(w, limiter.RetryAfterSeconds(clientIP))
			return
		}

		if credential.malformed || credential.ambiguous || credential.value == "" || credential.unsupportedTransport {
			// A malformed, ambiguous, or empty explicit credential -- or one
			// using a removed (query/form) transport -- is one
			// client-attributable authentication failure for this request,
			// regardless of how many conflicting values or transports were
			// involved. A removed transport's mere presence is disqualifying
			// even when a simultaneously-present Authorization header would
			// otherwise have been valid: it never "wins" against an
			// unsupported-transport attempt.
			limiter.RecordFailure(clientIP)
			JSONError(w, http.StatusUnauthorized, "Invalid API Key")
			return
		}
		u, err := apiKeyLookup(credential.value)
		if err != nil {
			if isClientAttributableAPIAuthFailure(err) {
				limiter.RecordFailure(clientIP)
			}
			JSONError(w, http.StatusUnauthorized, "Invalid API Key")
			return
		}
		r = ctx.Set(r, "user", u)
		r = ctx.Set(r, apiAuthenticationMechanismContextKey, apiAuthenticationKey)
		r = ctx.Set(r, "user_id", u.Id)
		handler.ServeHTTP(w, r)
	})
}

// RequireLogin checks to see if the user is currently logged in.
// If not, the function returns a 302 redirect to the login page.
func RequireLogin(handler http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if u := ctx.Get(r, "user"); u != nil {
			// If a password change is required for the user, then redirect them
			// to the login page
			currentUser := u.(models.User)
			if currentUser.PasswordChangeRequired && r.URL.Path != "/reset_password" {
				q := r.URL.Query()
				q.Set("next", r.URL.Path)
				http.Redirect(w, r, fmt.Sprintf("/reset_password?%s", q.Encode()), http.StatusTemporaryRedirect)
				return
			}
			handler.ServeHTTP(w, r)
			return
		}
		q := r.URL.Query()
		q.Set("next", r.URL.Path)
		http.Redirect(w, r, fmt.Sprintf("/login?%s", q.Encode()), http.StatusTemporaryRedirect)
	}
}

// EnforceViewOnly is a global middleware that limits the ability to edit
// objects to accounts with the PermissionModifyObjects permission.
func EnforceViewOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If the request is for any non-GET HTTP method, e.g. POST, PUT,
		// or DELETE, we need to ensure the user has the appropriate
		// permission.
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			user := ctx.Get(r, "user").(models.User)
			access, err := user.HasPermission(models.PermissionModifyObjects)
			if err != nil {
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			if !access {
				http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// RequirePermission checks to see if the user has the requested permission
// before executing the handler. If the request is unauthorized, a JSONError
// is returned.
func RequirePermission(perm string) func(http.Handler) http.HandlerFunc {
	return func(next http.Handler) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			user := ctx.Get(r, "user").(models.User)
			access, err := user.HasPermission(perm)
			if err != nil {
				JSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if !access {
				JSONError(w, http.StatusForbidden, http.StatusText(http.StatusForbidden))
				return
			}
			next.ServeHTTP(w, r)
		}
	}
}

// ApplySecurityHeaders applies various security headers according to best-
// practices.
func ApplySecurityHeaders(next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		csp := "frame-ancestors 'none';"
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	}
}

// JSONError returns an error in JSON format with the given
// status code and message
func JSONError(w http.ResponseWriter, c int, m string) {
	cj, _ := json.MarshalIndent(models.Response{Success: false, Message: m}, "", "  ")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(c)
	// Headers are committed: a write failure means the client went away.
	_, _ = fmt.Fprintf(w, "%s", cj)
}
