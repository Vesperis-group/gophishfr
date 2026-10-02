package middleware

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestFormTransportPreservesBusinessFields is the executable proof behind
// the migration guide's generic form template
// (docs/API_KEY_TRANSPORT_DEPRECATION.md, "Generic form migration template").
// It wraps the real RequireAPIKey middleware -- the same middleware every
// /api/* route uses -- around a local handler that only reads r.PostForm,
// and asserts unrelated business fields survive parsing and authentication
// byte-for-byte identical whether the request authenticates via the
// deprecated form api_key transport or the canonical Authorization: Bearer
// header with api_key removed from the body. No GophishFR endpoint actually
// accepts form-encoded business data; this test isolates RequireAPIKey's own
// contract from any particular endpoint's business logic, which is exactly
// what the generic template's migration rule depends on.
func TestFormTransportPreservesBusinessFields(t *testing.T) {
	testCtx := setupTest(t)

	businessFields := url.Values{
		"your_field":    {"unchanged-value"},
		"another_field": {"42"},
	}

	var captured url.Values
	captureHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.PostForm
		w.WriteHeader(http.StatusOK)
	})

	t.Run("deprecated form api_key preserves business fields", func(t *testing.T) {
		captured = nil
		body := cloneValues(businessFields)
		body.Set("api_key", testCtx.apiKey)

		request := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader(body.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()

		RequireAPIKey(captureHandler).ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", response.Code, response.Body.String())
		}
		assertBusinessFieldsUnchanged(t, businessFields, captured)
	})

	t.Run("canonical Bearer with api_key removed preserves the identical business fields", func(t *testing.T) {
		captured = nil
		body := cloneValues(businessFields)

		request := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader(body.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Authorization", "Bearer "+testCtx.apiKey)
		response := httptest.NewRecorder()

		RequireAPIKey(captureHandler).ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", response.Code, response.Body.String())
		}
		assertBusinessFieldsUnchanged(t, businessFields, captured)
	})
}

// cloneValues returns a deep-enough copy of v so a test can mutate the copy
// (e.g. adding "api_key") without affecting the shared expected-value fixture.
func cloneValues(v url.Values) url.Values {
	clone := make(url.Values, len(v))
	for key, values := range v {
		clone[key] = append([]string(nil), values...)
	}
	return clone
}

// assertBusinessFieldsUnchanged fails the test unless every field in want is
// present in got with byte-for-byte identical values, in the same order.
func assertBusinessFieldsUnchanged(t *testing.T, want, got url.Values) {
	t.Helper()
	for key, wantValues := range want {
		gotValues, ok := got[key]
		if !ok {
			t.Fatalf("expected PostForm to retain field %q, it was missing entirely: %v", key, got)
		}
		if len(gotValues) != len(wantValues) {
			t.Fatalf("field %q: expected %v, got %v", key, wantValues, gotValues)
		}
		for i := range wantValues {
			if gotValues[i] != wantValues[i] {
				t.Fatalf("field %q value %d: expected %q byte-for-byte, got %q", key, i, wantValues[i], gotValues[i])
			}
		}
	}
}
