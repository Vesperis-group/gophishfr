package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	csrf "filippo.io/csrf/gorilla"
	ctx "github.com/Vesperis-group/gophishfr/context"
	"github.com/Vesperis-group/gophishfr/models"
)

func TestExplicitAPICredentialExtraction(t *testing.T) {
	testCtx := setupTest(t)

	tests := []struct {
		name        string
		makeRequest func() *http.Request
		wantStatus  int
	}{
		{
			name: "raw authorization",
			makeRequest: func() *http.Request {
				request := httptest.NewRequest(http.MethodGet, "/api/test", nil)
				request.Header.Set("Authorization", testCtx.apiKey)
				return request
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "bearer authorization",
			makeRequest: func() *http.Request {
				request := httptest.NewRequest(http.MethodGet, "/api/test", nil)
				request.Header.Set("Authorization", "Bearer "+testCtx.apiKey)
				return request
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "query parameter",
			makeRequest: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "/api/test?api_key="+url.QueryEscape(testCtx.apiKey), nil)
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "form parameter",
			makeRequest: func() *http.Request {
				request := httptest.NewRequest(
					http.MethodPost,
					"/api/test",
					strings.NewReader(url.Values{"api_key": {testCtx.apiKey}}.Encode()),
				)
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				return request
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "identical repeated query parameters",
			makeRequest: func() *http.Request {
				values := url.Values{"api_key": {testCtx.apiKey, testCtx.apiKey}}
				return httptest.NewRequest(http.MethodGet, "/api/test?"+values.Encode(), nil)
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "identical repeated authorization headers",
			makeRequest: func() *http.Request {
				request := httptest.NewRequest(http.MethodGet, "/api/test", nil)
				request.Header.Add("Authorization", "Bearer "+testCtx.apiKey)
				request.Header.Add("Authorization", "Bearer "+testCtx.apiKey)
				return request
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "identical values across every source",
			makeRequest: func() *http.Request {
				values := url.Values{"api_key": {testCtx.apiKey, testCtx.apiKey}}
				request := httptest.NewRequest(
					http.MethodPost,
					"/api/test?"+values.Encode(),
					strings.NewReader(values.Encode()),
				)
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				request.Header.Add("Authorization", testCtx.apiKey)
				return request
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "distinct repeated query parameters",
			makeRequest: func() *http.Request {
				values := url.Values{"api_key": {testCtx.apiKey, "different-test-value"}}
				return httptest.NewRequest(http.MethodGet, "/api/test?"+values.Encode(), nil)
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "distinct repeated form parameters",
			makeRequest: func() *http.Request {
				values := url.Values{"api_key": {testCtx.apiKey, "different-test-value"}}
				request := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader(values.Encode()))
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				return request
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "distinct repeated authorization headers",
			makeRequest: func() *http.Request {
				request := httptest.NewRequest(http.MethodGet, "/api/test", nil)
				request.Header.Add("Authorization", "Bearer "+testCtx.apiKey)
				request.Header.Add("Authorization", "different-test-value")
				return request
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "query and form conflict",
			makeRequest: func() *http.Request {
				request := httptest.NewRequest(
					http.MethodPost,
					"/api/test?api_key="+url.QueryEscape(testCtx.apiKey),
					strings.NewReader(url.Values{"api_key": {"different-test-value"}}.Encode()),
				)
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				return request
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "authorization and query conflict",
			makeRequest: func() *http.Request {
				request := httptest.NewRequest(
					http.MethodGet,
					"/api/test?api_key="+url.QueryEscape(testCtx.apiKey),
					nil,
				)
				request.Header.Set("Authorization", "different-test-value")
				return request
			},
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			RequireAPIKey(successHandler).ServeHTTP(response, test.makeRequest())
			if response.Code != test.wantStatus {
				t.Fatalf("expected status %d, got %d", test.wantStatus, response.Code)
			}
		})
	}
}

func TestLockedAccountsRejectEveryAPIKeyTransport(t *testing.T) {
	testCtx := setupTest(t)
	admin, err := models.GetUser(1)
	if err != nil {
		t.Fatal(err)
	}
	role, err := models.GetRoleBySlug(models.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	lockedUser := models.User{
		Username:      "locked-api-transport-user",
		Hash:          "synthetic-locked-user-hash",
		RoleID:        role.ID,
		AccountLocked: true,
	}
	lockedUserToken := createTestUserWithAPIKey(t, &lockedUser)
	admin.AccountLocked = true
	if err := models.PutUser(&admin); err != nil {
		t.Fatal(err)
	}

	requests := []struct {
		name string
		make func(string) *http.Request
	}{
		{
			name: "bearer",
			make: func(token string) *http.Request {
				request := httptest.NewRequest(http.MethodGet, "/api/test", nil)
				request.Header.Set("Authorization", "Bearer "+token)
				return request
			},
		},
		{
			name: "raw",
			make: func(token string) *http.Request {
				request := httptest.NewRequest(http.MethodGet, "/api/test", nil)
				request.Header.Set("Authorization", token)
				return request
			},
		},
		{
			name: "query",
			make: func(token string) *http.Request {
				return httptest.NewRequest(http.MethodGet, "/api/test?api_key="+url.QueryEscape(token), nil)
			},
		},
		{
			name: "form",
			make: func(token string) *http.Request {
				request := httptest.NewRequest(
					http.MethodPost, "/api/test",
					strings.NewReader(url.Values{"api_key": {token}}.Encode()),
				)
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				return request
			},
		},
	}

	invalidResponse := httptest.NewRecorder()
	invalidRequest := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	invalidRequest.Header.Set("Authorization", "synthetic-invalid-locked-comparison")
	RequireAPIKey(successHandler).ServeHTTP(invalidResponse, invalidRequest)

	for _, identity := range []struct {
		name  string
		token string
	}{
		{name: "admin", token: testCtx.apiKey},
		{name: "user", token: lockedUserToken},
	} {
		for _, transport := range requests {
			t.Run(identity.name+"/"+transport.name, func(t *testing.T) {
				response := httptest.NewRecorder()
				RequireAPIKey(successHandler).ServeHTTP(response, transport.make(identity.token))
				if response.Code != http.StatusUnauthorized {
					t.Fatalf("locked account authenticated with status %d", response.Code)
				}
				if response.Body.String() != invalidResponse.Body.String() {
					t.Fatalf("locked-account response differs from invalid-key response: %q", response.Body.String())
				}
			})
		}
	}

	// Issuing a replacement while locked is allowed for session administration,
	// but it must not make API-key authentication usable.
	resetToken, err := models.ResetUserAPIKey(lockedUser.Id)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	RequireAPIKey(successHandler).ServeHTTP(response, requests[0].make(resetToken))
	if response.Code != http.StatusUnauthorized || response.Body.String() != invalidResponse.Body.String() {
		t.Fatal("reset made a locked user's API key usable or revealed lock state")
	}

	admin.AccountLocked = false
	if err := models.PutUser(&admin); err != nil {
		t.Fatal(err)
	}
	lockedUser.AccountLocked = false
	if err := models.PutUser(&lockedUser); err != nil {
		t.Fatal(err)
	}
	for name, token := range map[string]string{"admin": testCtx.apiKey, "user": resetToken} {
		t.Run("unlock/"+name, func(t *testing.T) {
			response := httptest.NewRecorder()
			RequireAPIKey(successHandler).ServeHTTP(response, requests[0].make(token))
			if response.Code != http.StatusOK {
				t.Fatalf("unlock did not restore same token: %d %s", response.Code, response.Body.String())
			}
		})
	}

	// Session selection intentionally remains unchanged by API-key lock policy.
	admin.AccountLocked = true
	admin.PasswordChangeRequired = false
	sessionRequest := ctx.Set(httptest.NewRequest(http.MethodGet, "/api/test", nil), "user", admin)
	sessionResponse := httptest.NewRecorder()
	RequireAPIKey(successHandler).ServeHTTP(sessionResponse, sessionRequest)
	if sessionResponse.Code != http.StatusOK {
		t.Fatal("API-key lock enforcement changed existing session selection behavior")
	}
}

func TestAPIAuthenticationMechanismSelection(t *testing.T) {
	testCtx := setupTest(t)
	sessionUser, err := models.GetUser(1)
	if err != nil {
		t.Fatal(err)
	}
	sessionUser.PasswordChangeRequired = false
	apiUser := models.User{
		Username: "api-identity-test-user",
		RoleID:   sessionUser.RoleID,
	}
	apiUserKey := createTestUserWithAPIKey(t, &apiUser)

	identityHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := ctx.Get(r, "user").(models.User)
		if !ok {
			t.Fatal("authenticated request has no user context")
		}
		userID, ok := ctx.Get(r, "user_id").(int64)
		if !ok || userID != user.Id {
			t.Fatal("authenticated request has inconsistent user_id context")
		}
		_, _ = w.Write([]byte(user.Username))
	})

	tests := []struct {
		name         string
		target       string
		header       *string
		form         url.Values
		rawForm      string
		session      interface{}
		wantStatus   int
		wantIdentity string
	}{
		{
			name:         "valid key without session",
			target:       "/api/test",
			header:       stringPointer("Bearer " + testCtx.apiKey),
			wantStatus:   http.StatusOK,
			wantIdentity: sessionUser.Username,
		},
		{
			name:         "valid key with different session selects key identity",
			target:       "/api/test",
			header:       stringPointer(apiUserKey),
			session:      sessionUser,
			wantStatus:   http.StatusOK,
			wantIdentity: apiUser.Username,
		},
		{
			name:       "invalid key with valid session cannot fall back",
			target:     "/api/test",
			header:     stringPointer("invalid-explicit-test-value"),
			session:    sessionUser,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "empty authorization with valid session cannot fall back",
			target:     "/api/test",
			header:     stringPointer(""),
			session:    sessionUser,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "empty query with valid session cannot fall back",
			target:     "/api/test?api_key=",
			session:    sessionUser,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "empty form with valid session cannot fall back",
			target:     "/api/test",
			form:       url.Values{"api_key": {""}},
			session:    sessionUser,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "malformed query with valid session cannot fall back",
			target:     "/api/test?api_key=%zz",
			session:    sessionUser,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "malformed form with valid session cannot fall back",
			target:     "/api/test",
			rawForm:    "api_key=%zz",
			session:    sessionUser,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:         "no explicit credential uses valid session",
			target:       "/api/test",
			session:      sessionUser,
			wantStatus:   http.StatusOK,
			wantIdentity: sessionUser.Username,
		},
		{
			name:       "no explicit credential and no session fails",
			target:     "/api/test",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "invalid session context fails without panic",
			target:     "/api/test",
			session:    &sessionUser,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "conflict with valid session cannot fall back",
			target:     "/api/test?api_key=" + url.QueryEscape(testCtx.apiKey),
			header:     stringPointer(apiUserKey),
			session:    sessionUser,
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var body io.Reader
			method := http.MethodGet
			if test.form != nil {
				method = http.MethodPost
				body = strings.NewReader(test.form.Encode())
			} else if test.rawForm != "" {
				method = http.MethodPost
				body = strings.NewReader(test.rawForm)
			}
			request := httptest.NewRequest(method, test.target, body)
			if test.form != nil || test.rawForm != "" {
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			if test.header != nil {
				request.Header["Authorization"] = []string{*test.header}
			}
			if test.session != nil {
				request = ctx.Set(request, "user", test.session)
			}
			response := httptest.NewRecorder()
			RequireAPIKey(identityHandler).ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("expected status %d, got %d", test.wantStatus, response.Code)
			}
			if test.wantIdentity != "" && response.Body.String() != test.wantIdentity {
				t.Fatalf("expected identity %q, got %q", test.wantIdentity, response.Body.String())
			}
			if test.wantStatus == http.StatusUnauthorized {
				if response.Header().Get("Content-Type") != "application/json" {
					t.Fatalf("authentication failure is not JSON: %q", response.Header().Get("Content-Type"))
				}
				if response.Header().Get("Location") != "" {
					t.Fatal("API authentication failure redirected")
				}
			}
		})
	}
}

func TestCredentialAwareAPIProtection(t *testing.T) {
	protected := CSRFExceptions(csrf.Protect(
		[]byte("ignored-by-cross-origin-protection"),
		csrf.ErrorHandler(CSRFFailureHandler),
	)(successHandler))

	tests := []struct {
		name        string
		method      string
		headerName  string
		headerValue string
		wantStatus  int
	}{
		{
			name:        "session unsafe same-origin",
			method:      http.MethodPost,
			headerName:  "Sec-Fetch-Site",
			headerValue: "same-origin",
			wantStatus:  http.StatusOK,
		},
		{
			name:        "session unsafe cross-site",
			method:      http.MethodPost,
			headerName:  "Sec-Fetch-Site",
			headerValue: "cross-site",
			wantStatus:  http.StatusForbidden,
		},
		{
			name:        "session unsafe same-site",
			method:      http.MethodPut,
			headerName:  "Sec-Fetch-Site",
			headerValue: "same-site",
			wantStatus:  http.StatusForbidden,
		},
		{
			name:        "session unsafe foreign origin",
			method:      http.MethodDelete,
			headerName:  "Origin",
			headerValue: "https://foreign.invalid",
			wantStatus:  http.StatusForbidden,
		},
		{
			name:        "explicit credential retains unsafe exemption",
			method:      http.MethodPost,
			headerName:  "Authorization",
			headerValue: "invalid-but-explicit-test-value",
			wantStatus:  http.StatusOK,
		},
		{
			name:        "empty explicit credential retains unsafe exemption",
			method:      http.MethodPost,
			headerName:  "Authorization",
			headerValue: "",
			wantStatus:  http.StatusOK,
		},
		{
			name:        "safe session method",
			method:      http.MethodGet,
			headerName:  "Sec-Fetch-Site",
			headerValue: "cross-site",
			wantStatus:  http.StatusOK,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "http://example.com/api/test", nil)
			request.Header[test.headerName] = []string{test.headerValue}
			response := httptest.NewRecorder()
			protected.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("expected status %d, got %d", test.wantStatus, response.Code)
			}
			if test.wantStatus == http.StatusForbidden {
				if response.Header().Get("Content-Type") != "application/json" {
					t.Fatalf("CSRF failure is not JSON: %q", response.Header().Get("Content-Type"))
				}
				if response.Header().Get("Location") != "" {
					t.Fatal("CSRF failure redirected")
				}
			}
		})
	}
}

func TestSessionPasswordChangeRequired(t *testing.T) {
	testCtx := setupTest(t)
	user, err := models.GetUser(1)
	if err != nil {
		t.Fatal(err)
	}
	if !user.PasswordChangeRequired {
		t.Fatal("test requires an initial forced-password-change user")
	}

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run("session "+method+" is rejected", func(t *testing.T) {
			called := false
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
			})
			request := httptest.NewRequest(method, "/api/test", nil)
			request = ctx.Set(request, "user", user)
			response := httptest.NewRecorder()
			RequireAPIKey(EnforceViewOnly(handler)).ServeHTTP(response, request)

			if response.Code != http.StatusForbidden {
				t.Fatalf("expected 403, got %d", response.Code)
			}
			if called {
				t.Fatal("forced-password-change session reached the API handler")
			}
			if response.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("expected JSON response, got %q", response.Header().Get("Content-Type"))
			}
			if response.Header().Get("Location") != "" {
				t.Fatal("forced-password-change session redirected")
			}
		})
	}

	t.Run("explicit API key remains compatible", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if IsSessionAuthentication(r) {
				t.Fatal("explicit API key was marked as session authentication")
			}
			w.WriteHeader(http.StatusNoContent)
		})
		request := httptest.NewRequest(http.MethodPost, "/api/test", nil)
		request.Header.Set("Authorization", testCtx.apiKey)
		response := httptest.NewRecorder()
		RequireAPIKey(handler).ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("expected explicit key success, got %d", response.Code)
		}
	})
}

func TestSessionAndAPIKeyRBACParity(t *testing.T) {
	testCtx := setupTest(t)
	admin, err := models.GetUser(1)
	if err != nil {
		t.Fatal(err)
	}
	admin.PasswordChangeRequired = false
	viewOnly := models.User{
		Username: "view-only-auth-parity-user",
		RoleID:   999999,
	}
	viewOnlyKey := createTestUserWithAPIKey(t, &viewOnly)

	for _, mechanism := range []string{"session", "api-key"} {
		t.Run(mechanism+" view-only mutation denied", func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/test", nil)
			if mechanism == "session" {
				request = ctx.Set(request, "user", viewOnly)
			} else {
				request.Header.Set("Authorization", viewOnlyKey)
			}
			response := httptest.NewRecorder()
			RequireAPIKey(EnforceViewOnly(successHandler)).ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("expected view-only mutation to be forbidden, got %d", response.Code)
			}
		})

		t.Run(mechanism+" system permission denied", func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/test", nil)
			if mechanism == "session" {
				request = ctx.Set(request, "user", viewOnly)
			} else {
				request.Header.Set("Authorization", viewOnlyKey)
			}
			response := httptest.NewRecorder()
			handler := RequireAPIKey(RequirePermission(models.PermissionModifySystem)(successHandler))
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("expected system permission denial, got %d", response.Code)
			}
		})

		t.Run(mechanism+" admin mutation allowed", func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/test", nil)
			if mechanism == "session" {
				request = ctx.Set(request, "user", admin)
			} else {
				request.Header.Set("Authorization", testCtx.apiKey)
			}
			response := httptest.NewRecorder()
			RequireAPIKey(EnforceViewOnly(successHandler)).ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("expected admin mutation to succeed, got %d", response.Code)
			}
		})
	}
}

func TestInvalidStoredSessionIdentityFailsWithoutPanic(t *testing.T) {
	setupTest(t)
	initialRequest := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	initialResponse := httptest.NewRecorder()
	session, err := Store.Get(initialRequest, "gophish")
	if err != nil {
		t.Fatal(err)
	}
	session.Values["id"] = "invalid-session-identity-type"
	if err := session.Save(initialRequest, initialResponse); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	for _, cookie := range initialResponse.Result().Cookies() {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	GetContext(RequireAPIKey(successHandler)).ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected invalid session identity to return 401, got %d", response.Code)
	}
}

func stringPointer(value string) *string {
	return &value
}
