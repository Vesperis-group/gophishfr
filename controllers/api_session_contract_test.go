package controllers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Vesperis-group/gophishfr/auth"
	mid "github.com/Vesperis-group/gophishfr/middleware"
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
	if loginResponse.Request.URL.Path != "/reset_password" {
		t.Fatalf("initial login did not enter forced password reset: %s", loginResponse.Request.URL.Path)
	}
	admin, err := models.GetUser(1)
	if err != nil {
		t.Fatal(err)
	}
	initialVerifier := append([]byte(nil), admin.APIKeyVerifier...)

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

	t.Run("forced password change blocks session API GET", func(t *testing.T) {
		response := doRequest(http.MethodGet, "/api/groups/summary", nil, nil)
		defer func() { _ = response.Body.Close() }()
		assertAPIResponse(t, response, http.StatusForbidden)
		if response.Request.URL.Path != "/api/groups/summary" {
			t.Fatal("forced-password-change API request redirected")
		}
	})

	t.Run("forced password change blocks session API mutation", func(t *testing.T) {
		response := doRequest(http.MethodPost, "/api/reset", nil, map[string]string{
			"Sec-Fetch-Site": "same-origin",
		})
		defer func() { _ = response.Body.Close() }()
		assertAPIResponse(t, response, http.StatusForbidden)
		current, err := models.GetUser(1)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(current.APIKeyVerifier, initialVerifier) {
			t.Fatal("forced-password-change session mutated the API key")
		}
	})

	t.Run("forced password reset enables session API", func(t *testing.T) {
		changedPassword := "synthetic-session-contract-reset"
		response := doRequest(
			http.MethodPost,
			"/reset_password",
			strings.NewReader(url.Values{
				"password":         {changedPassword},
				"confirm_password": {changedPassword},
			}.Encode()),
			map[string]string{
				"Content-Type":   "application/x-www-form-urlencoded",
				"Sec-Fetch-Site": "same-origin",
			},
		)
		defer func() { _ = response.Body.Close() }()

		current, err := models.GetUser(1)
		if err != nil {
			t.Fatal(err)
		}
		if current.PasswordChangeRequired {
			t.Fatal("successful reset did not clear the forced-password-change state")
		}
		if err := auth.ValidatePassword(changedPassword, current.Hash); err != nil {
			t.Fatal("successful reset did not persist the replacement password")
		}
	})

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
			"Authorization":  "Bearer " + testCtx.apiKey,
			"Content-Type":   "application/json",
			"Sec-Fetch-Site": "cross-site",
		})
		defer func() { _ = response.Body.Close() }()
		assertAPIResponse(t, response, http.StatusCreated)
	})

	t.Run("distinct explicit credentials are rejected", func(t *testing.T) {
		response := doRequest(
			http.MethodGet,
			"/api/groups/summary?api_key="+url.QueryEscape(testCtx.apiKey),
			nil,
			map[string]string{"Authorization": "different-explicit-test-value"},
		)
		defer func() { _ = response.Body.Close() }()
		assertAPIResponse(t, response, http.StatusUnauthorized)
	})

	sessionCampaign := createCompletionContractCampaign(t, "Session completion contract")
	assertCampaignIncomplete := func() {
		t.Helper()
		campaign, err := models.GetCampaign(sessionCampaign.Id, 1)
		if err != nil {
			t.Fatal(err)
		}
		if campaign.Status == models.CampaignComplete {
			t.Fatal("rejected completion request mutated the campaign")
		}
	}

	t.Run("session campaign completion GET is rejected without mutation", func(t *testing.T) {
		response := doRequest(
			http.MethodGet,
			fmt.Sprintf("/api/campaigns/%d/complete", sessionCampaign.Id),
			nil,
			nil,
		)
		defer func() { _ = response.Body.Close() }()
		assertAPIResponse(t, response, http.StatusMethodNotAllowed)
		assertCampaignIncomplete()
	})

	t.Run("cross-site session campaign completion GET is rejected without mutation", func(t *testing.T) {
		response := doRequest(
			http.MethodGet,
			fmt.Sprintf("/api/campaigns/%d/complete", sessionCampaign.Id),
			nil,
			map[string]string{"Sec-Fetch-Site": "cross-site"},
		)
		defer func() { _ = response.Body.Close() }()
		assertAPIResponse(t, response, http.StatusMethodNotAllowed)
		assertCampaignIncomplete()
	})

	t.Run("cross-origin session campaign completion POST is rejected", func(t *testing.T) {
		response := doRequest(
			http.MethodPost,
			fmt.Sprintf("/api/campaigns/%d/complete", sessionCampaign.Id),
			nil,
			map[string]string{"Origin": "https://foreign.invalid"},
		)
		defer func() { _ = response.Body.Close() }()
		assertAPIResponse(t, response, http.StatusForbidden)
		assertCampaignIncomplete()
	})

	t.Run("view-only session campaign completion POST is rejected", func(t *testing.T) {
		hash, err := auth.GeneratePasswordHash("synthetic-view-only-session-password")
		if err != nil {
			t.Fatal(err)
		}
		viewOnly := models.User{
			Username: "session-completion-view-only",
			Hash:     hash,
			RoleID:   999999,
		}
		if _, err := models.CreateUserWithAPIKey(&viewOnly); err != nil {
			t.Fatal(err)
		}
		viewJar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatal(err)
		}
		viewClient := &http.Client{Jar: viewJar}
		cookieRequest, err := http.NewRequest(http.MethodGet, testCtx.adminServer.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		session, err := mid.Store.Get(cookieRequest, "gophish")
		if err != nil {
			t.Fatal(err)
		}
		session.Values["id"] = viewOnly.Id
		cookieResponse := httptest.NewRecorder()
		if err := session.Save(cookieRequest, cookieResponse); err != nil {
			t.Fatal(err)
		}
		serverURL, err := url.Parse(testCtx.adminServer.URL)
		if err != nil {
			t.Fatal(err)
		}
		cookieResult := cookieResponse.Result()
		defer func() { _ = cookieResult.Body.Close() }()
		viewJar.SetCookies(serverURL, cookieResult.Cookies())

		request, err := http.NewRequest(
			http.MethodPost,
			fmt.Sprintf("%s/api/campaigns/%d/complete", testCtx.adminServer.URL, sessionCampaign.Id),
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Sec-Fetch-Site", "same-origin")
		response, err := viewClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("expected view-only 403, got %d", response.StatusCode)
		}
		assertCampaignIncomplete()
	})

	t.Run("same-origin session campaign completion POST succeeds", func(t *testing.T) {
		response := doRequest(
			http.MethodPost,
			fmt.Sprintf("/api/campaigns/%d/complete", sessionCampaign.Id),
			nil,
			map[string]string{"Sec-Fetch-Site": "same-origin"},
		)
		defer func() { _ = response.Body.Close() }()
		assertAPIResponse(t, response, http.StatusOK)
		campaign, err := models.GetCampaign(sessionCampaign.Id, 1)
		if err != nil {
			t.Fatal(err)
		}
		if campaign.Status != models.CampaignComplete {
			t.Fatal("same-origin session POST did not complete campaign")
		}
	})

	t.Run("explicit API key campaign completion GET remains compatible", func(t *testing.T) {
		campaign := createCompletionContractCampaign(t, "Explicit completion contract")
		response := doRequest(
			http.MethodGet,
			fmt.Sprintf("/api/campaigns/%d/complete", campaign.Id),
			nil,
			map[string]string{
				"Authorization":  testCtx.apiKey,
				"Sec-Fetch-Site": "cross-site",
			},
		)
		defer func() { _ = response.Body.Close() }()
		assertAPIResponse(t, response, http.StatusOK)
		completed, err := models.GetCampaign(campaign.Id, 1)
		if err != nil {
			t.Fatal(err)
		}
		if completed.Status != models.CampaignComplete {
			t.Fatal("legacy explicit-key GET did not complete campaign")
		}
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
			"Authorization": testCtx.apiKey,
		})
		defer func() { _ = response.Body.Close() }()
		assertAPIResponse(t, response, http.StatusOK)
	})
}

func createCompletionContractCampaign(t *testing.T, name string) models.Campaign {
	t.Helper()
	group, err := models.GetGroup(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	template, err := models.GetTemplate(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	page, err := models.GetPage(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	smtp, err := models.GetSMTP(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	campaign := models.Campaign{
		Name:     name,
		UserId:   1,
		Groups:   []models.Group{group},
		Template: template,
		Page:     page,
		SMTP:     smtp,
		URL:      "http://localhost.invalid",
	}
	if err := models.PostCampaign(&campaign, 1); err != nil {
		t.Fatal(err)
	}
	if err := campaign.UpdateStatus(models.CampaignEmailsSent); err != nil {
		t.Fatal(err)
	}
	return campaign
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
