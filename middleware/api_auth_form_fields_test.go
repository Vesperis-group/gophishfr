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
// /api/* route uses -- around a local handler that only reads r.PostForm.
// The deprecated form api_key transport was removed in 0.13.0 (see
// docs/API_KEY_TRANSPORT_DEPRECATION.md): a form request carrying it is
// rejected before any business-field-dependent handler logic ever runs, so
// a form request WITHOUT api_key is the only one that can still prove
// business-field preservation through RequireAPIKey, via the canonical
// Authorization: Bearer header with api_key removed from the body.
func TestFormTransportPreservesBusinessFields(t *testing.T) {
	testCtx := setupTest(t)

	businessFields := url.Values{
		"your_field":    {"unchanged-value"},
		"another_field": {"42"},
	}

	var captured url.Values
	handlerCalled := false
	captureHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		captured = r.PostForm
		w.WriteHeader(http.StatusOK)
	})

	t.Run("deprecated form api_key is rejected before any business-field handler logic runs", func(t *testing.T) {
		captured = nil
		handlerCalled = false
		body := cloneValues(businessFields)
		body.Set("api_key", testCtx.apiKey)

		request := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader(body.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()

		RequireAPIKey(captureHandler).ServeHTTP(response, request)

		if response.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 for a removed form api_key transport, got %d: %s", response.Code, response.Body.String())
		}
		if handlerCalled {
			t.Fatal("a removed form api_key transport must never reach the business-field handler")
		}
	})

	t.Run("canonical Bearer with api_key removed preserves the identical business fields", func(t *testing.T) {
		captured = nil
		handlerCalled = false
		body := cloneValues(businessFields)

		request := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader(body.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Authorization", "Bearer "+testCtx.apiKey)
		response := httptest.NewRecorder()

		RequireAPIKey(captureHandler).ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", response.Code, response.Body.String())
		}
		if !handlerCalled {
			t.Fatal("expected the business-field handler to run for the canonical transport")
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
