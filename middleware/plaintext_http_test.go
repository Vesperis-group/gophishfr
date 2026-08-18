package middleware

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/csrf"
)

// TestPlaintextHTTP covers the transport detection that decides whether
// gorilla/csrf enforces its strict Referer check. Marking a TLS-terminated
// deployment as plaintext would make csrf compare an "https" Origin against an
// "http" request URL and reject every legitimate browser POST, so both
// directions matter.
func TestPlaintextHTTP(t *testing.T) {
	tests := []struct {
		name          string
		mutate        func(r *http.Request)
		wantPlaintext bool
	}{
		{
			name:          "cleartext http",
			mutate:        func(r *http.Request) {},
			wantPlaintext: true,
		},
		{
			name:          "direct TLS",
			mutate:        func(r *http.Request) { r.TLS = &tls.ConnectionState{} },
			wantPlaintext: false,
		},
		{
			// ProxyHeaders resolves X-Forwarded-Proto into r.URL.Scheme before
			// this middleware runs.
			name:          "TLS terminated at a reverse proxy",
			mutate:        func(r *http.Request) { r.URL.Scheme = "https" },
			wantPlaintext: false,
		},
		{
			name:          "reverse proxy sending an uppercased scheme",
			mutate:        func(r *http.Request) { r.URL.Scheme = "HTTPS" },
			wantPlaintext: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got interface{}
			handler := PlaintextHTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Context().Value(csrf.PlaintextHTTPContextKey)
			}))

			req := httptest.NewRequest(http.MethodPost, "/login", nil)
			tt.mutate(req)
			handler.ServeHTTP(httptest.NewRecorder(), req)

			if !tt.wantPlaintext {
				if got != nil {
					t.Fatalf("request was marked as plaintext HTTP, disabling the strict Referer check")
				}
				return
			}

			plaintext, ok := got.(bool)
			if !ok || !plaintext {
				t.Fatalf("cleartext request was not marked as plaintext HTTP: got %v", got)
			}
		})
	}
}
