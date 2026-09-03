package controllers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"github.com/Vesperis-group/gophishfr/models"
)

func TestSessionAPIAuthenticationContract(t *testing.T) {
	testCtx := setupTest(t)
	defer tearDown(t, testCtx)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	loginResponse := attemptLogin(t, testCtx, client, "admin", "gophish", "")
	_ = loginResponse.Body.Close()
	admin, err := models.GetUser(1)
	if err != nil {
		t.Fatal(err)
	}
	admin.PasswordChangeRequired = false
	if err := models.PutUser(&admin); err != nil {
		t.Fatal(err)
	}

	doRequest := func(method, path string, body io.Reader, headers map[string]string) *http.Response {
		t.Helper()
		request, err := http.NewRequest(method, testCtx.adminServer.URL+path, body)
		if err != nil {
			t.Fatal(err)
		}
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	t.Run("GET uses the existing session", func(t *testing.T) {
		response := doRequest(http.MethodGet, "/api/groups/summary", nil, nil)
		defer func() { _ = response.Body.Close() }()
		assertAPIResponse(t, response, http.StatusOK)
	})

	var created models.Group
	t.Run("POST uses the existing session with same-origin protection", func(t *testing.T) {
		payload := []byte(`{"name":"Session contract group","targets":[{"email":"session-contract@example.invalid"}]}`)
		response := doRequest(http.MethodPost, "/api/groups/", bytes.NewReader(payload), map[string]string{
			"Content-Type":   "application/json",
			"Sec-Fetch-Site": "same-origin",
		})
		defer func() { _ = response.Body.Close() }()
		assertAPIResponse(t, response, http.StatusCreated)
		if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
			t.Fatal(err)
		}
		if created.Id == 0 || created.UserId != 0 {
			t.Fatalf("unexpected created group response: %#v", created)
		}
	})

	t.Run("PUT uses the existing session", func(t *testing.T) {
		payload := fmt.Sprintf(
			`{"id":%d,"name":"Updated session contract group","targets":[{"email":"updated-session-contract@example.invalid"}]}`,
			created.Id,
		)
		response := doRequest(
			http.MethodPut,
			fmt.Sprintf("/api/groups/%d", created.Id),
			strings.NewReader(payload),
			map[string]string{"Content-Type": "application/json", "Sec-Fetch-Site": "same-origin"},
		)
		defer func() { _ = response.Body.Close() }()
		assertAPIResponse(t, response, http.StatusOK)
	})

	t.Run("DELETE uses the existing session", func(t *testing.T) {
		response := doRequest(
			http.MethodDelete,
			fmt.Sprintf("/api/groups/%d", created.Id),
			nil,
			map[string]string{"Sec-Fetch-Site": "same-origin"},
		)
		defer func() { _ = response.Body.Close() }()
		assertAPIResponse(t, response, http.StatusOK)
	})

	t.Run("cross-site session mutation is rejected", func(t *testing.T) {
		response := doRequest(http.MethodPost, "/api/reset", nil, map[string]string{
			"Sec-Fetch-Site": "cross-site",
		})
		defer func() { _ = response.Body.Close() }()
		assertAPIResponse(t, response, http.StatusForbidden)
	})

	t.Run("same-site session mutation is rejected", func(t *testing.T) {
		response := doRequest(http.MethodPost, "/api/reset", nil, map[string]string{
			"Sec-Fetch-Site": "same-site",
		})
		defer func() { _ = response.Body.Close() }()
		assertAPIResponse(t, response, http.StatusForbidden)
	})

	t.Run("foreign Origin session mutation is rejected", func(t *testing.T) {
		response := doRequest(http.MethodPost, "/api/reset", nil, map[string]string{
			"Origin": "https://foreign.invalid",
		})
		defer func() { _ = response.Body.Close() }()
		assertAPIResponse(t, response, http.StatusForbidden)
	})

	for _, test := range []struct {
		name    string
		path    string
		headers map[string]string
		body    io.Reader
	}{
		{
			name: "invalid authorization does not fall back",
			path: "/api/groups/summary",
			headers: map[string]string{
				"Authorization": "Bearer invalid-session-fallback-test-value",
			},
		},
		{
			name: "empty authorization does not fall back",
			path: "/api/groups/summary",
			headers: map[string]string{
				"Authorization": "",
			},
		},
		{name: "empty query does not fall back", path: "/api/groups/summary?api_key="},
		{name: "malformed query does not fall back", path: "/api/groups/summary?api_key=%zz"},
		{
			name: "empty form does not fall back",
			path: "/api/groups/summary",
			headers: map[string]string{
				"Content-Type": "application/x-www-form-urlencoded",
			},
			body: strings.NewReader("api_key="),
		},
		{
			name: "malformed form does not fall back",
			path: "/api/groups/summary",
			headers: map[string]string{
				"Content-Type": "application/x-www-form-urlencoded",
			},
			body: strings.NewReader("api_key=%zz"),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			method := http.MethodGet
			if test.body != nil {
				method = http.MethodPost
			}
			response := doRequest(method, test.path, test.body, test.headers)
			defer func() { _ = response.Body.Close() }()
			assertAPIResponse(t, response, http.StatusUnauthorized)
			if response.Request.URL.Path != strings.Split(test.path, "?")[0] {
				t.Fatal("API authentication failure redirected")
			}
		})
	}

	admin, err = models.GetUser(1)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("explicit API key retains unsafe cross-site compatibility", func(t *testing.T) {
		payload := []byte(`{"name":"Explicit key contract group","targets":[{"email":"api-key-contract@example.invalid"}]}`)
		response := doRequest(http.MethodPost, "/api/groups/", bytes.NewReader(payload), map[string]string{
			"Authorization":  "Bearer " + admin.ApiKey,
			"Content-Type":   "application/json",
			"Sec-Fetch-Site": "cross-site",
		})
		defer func() { _ = response.Body.Close() }()
		assertAPIResponse(t, response, http.StatusCreated)
	})

	t.Run("distinct explicit credentials are rejected", func(t *testing.T) {
		response := doRequest(
			http.MethodGet,
			"/api/groups/summary?api_key="+url.QueryEscape(admin.ApiKey),
			nil,
			map[string]string{"Authorization": "different-explicit-test-value"},
		)
		defer func() { _ = response.Body.Close() }()
		assertAPIResponse(t, response, http.StatusUnauthorized)
	})

	t.Run("logout invalidates session API authority", func(t *testing.T) {
		response := doRequest(http.MethodGet, "/logout", nil, nil)
		_ = response.Body.Close()

		response = doRequest(http.MethodGet, "/api/groups/summary", nil, nil)
		defer func() { _ = response.Body.Close() }()
		assertAPIResponse(t, response, http.StatusUnauthorized)
	})

	t.Run("explicit API key still works after logout", func(t *testing.T) {
		response := doRequest(http.MethodGet, "/api/groups/summary", nil, map[string]string{
			"Authorization": admin.ApiKey,
		})
		defer func() { _ = response.Body.Close() }()
		assertAPIResponse(t, response, http.StatusOK)
	})
}

func assertAPIResponse(t *testing.T, response *http.Response, expectedStatus int) {
	t.Helper()
	if response.StatusCode != expectedStatus {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("expected status %d, got %d: %s", expectedStatus, response.StatusCode, body)
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("expected JSON response, got %q", response.Header.Get("Content-Type"))
	}
}
