package controllers

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/Vesperis-group/gophishfr/auth"
	"github.com/Vesperis-group/gophishfr/models"
)

func attemptLogin(t *testing.T, ctx *testContext, client *http.Client, username, password, optionalPath string) *http.Response {
	resp, err := http.Get(fmt.Sprintf("%s/login", ctx.adminServer.URL))
	if err != nil {
		t.Fatalf("error requesting the /login endpoint: %v", err)
	}
	got := resp.StatusCode
	expected := http.StatusOK
	if got != expected {
		t.Fatalf("invalid status code received. expected %d got %d", expected, got)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		t.Fatalf("error parsing /login response body")
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("error closing /login response body: %v", err)
	}
	elem := doc.Find("input[name='csrf_token']").First()
	token, ok := elem.Attr("value")
	if !ok {
		t.Fatal("unable to find csrf_token value in login response")
	}
	if client == nil {
		client = &http.Client{}
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/login%s", ctx.adminServer.URL, optionalPath), strings.NewReader(url.Values{
		"username":   {username},
		"password":   {password},
		"csrf_token": {token},
	}.Encode()))
	if err != nil {
		t.Fatalf("error creating new /login request: %v", err)
	}

	req.Header.Set("Cookie", resp.Header.Get("Set-Cookie"))
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("error requesting the /login endpoint: %v", err)
	}
	return resp
}

// TestLoginCSRF asserts that cross-site form submissions to /login are
// rejected, and that same-origin ones are not.
//
// CSRF protection is same-origin enforcement driven by Fetch metadata headers
// rather than by tokens, so these headers are what has to be exercised. The
// previous version of this test posted without a token and expected 403; under
// this model a request carrying neither Sec-Fetch-Site nor Origin is not a
// browser request at all, and is allowed. Asserting on the token would have
// tested a mechanism that no longer defends anything.
func TestLoginCSRF(t *testing.T) {
	tests := []struct {
		name     string
		headers  map[string]string
		expected int
	}{
		{
			name:     "cross-site submission is rejected",
			headers:  map[string]string{"Sec-Fetch-Site": "cross-site"},
			expected: http.StatusForbidden,
		},
		{
			name:     "same-site submission is rejected",
			headers:  map[string]string{"Sec-Fetch-Site": "same-site"},
			expected: http.StatusForbidden,
		},
		{
			name:     "same-origin submission is allowed",
			headers:  map[string]string{"Sec-Fetch-Site": "same-origin"},
			expected: http.StatusOK,
		},
		{
			// Browsers predating Sec-Fetch-Site still send Origin on POST.
			name:     "foreign Origin is rejected",
			headers:  map[string]string{"Origin": "https://evil.example.com"},
			expected: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := setupTest(t)
			defer tearDown(t, ctx)

			req, err := http.NewRequest(http.MethodPost,
				fmt.Sprintf("%s/login", ctx.adminServer.URL),
				strings.NewReader(url.Values{
					"username": {"admin"},
					"password": {"gophish"},
				}.Encode()))
			if err != nil {
				t.Fatalf("error creating new /login request: %v", err)
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			for name, value := range tt.headers {
				req.Header.Set(name, value)
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("error requesting the /login endpoint: %v", err)
			}
			defer func() {
				if err := resp.Body.Close(); err != nil {
					t.Errorf("error closing /login response body: %v", err)
				}
			}()

			if got := resp.StatusCode; got != tt.expected {
				t.Fatalf("invalid status code received. expected %d got %d", tt.expected, got)
			}
		})
	}
}

func TestSettingsCSRF(t *testing.T) {
	tests := []struct {
		name         string
		secFetchSite string
		expected     int
	}{
		{
			name:         "cross-site authenticated submission is rejected",
			secFetchSite: "cross-site",
			expected:     http.StatusForbidden,
		},
		{
			name:         "same-origin submission reaches settings handler",
			secFetchSite: "same-origin",
			expected:     http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := setupTest(t)
			defer tearDown(t, ctx)

			jar, err := cookiejar.New(nil)
			if err != nil {
				t.Fatalf("error creating cookie jar: %v", err)
			}
			client := &http.Client{Jar: jar}
			loginResponse := attemptLogin(t, ctx, client, "admin", "gophish", "")
			if err := loginResponse.Body.Close(); err != nil {
				t.Fatalf("error closing login response body: %v", err)
			}

			request, err := http.NewRequest(
				http.MethodPost,
				fmt.Sprintf("%s/settings", ctx.adminServer.URL),
				strings.NewReader(url.Values{
					"current_password":     {"synthetic-invalid-password"},
					"new_password":         {""},
					"confirm_new_password": {""},
				}.Encode()),
			)
			if err != nil {
				t.Fatalf("error creating settings request: %v", err)
			}
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set("Sec-Fetch-Site", tt.secFetchSite)

			response, err := client.Do(request)
			if err != nil {
				t.Fatalf("error posting settings: %v", err)
			}
			defer func() {
				if err := response.Body.Close(); err != nil {
					t.Errorf("error closing settings response body: %v", err)
				}
			}()
			if response.StatusCode != tt.expected {
				t.Fatalf(
					"invalid status code received. expected %d got %d",
					tt.expected,
					response.StatusCode,
				)
			}
		})
	}
}

func TestInvalidCredentials(t *testing.T) {
	ctx := setupTest(t)
	defer tearDown(t, ctx)
	resp := attemptLogin(t, ctx, nil, "admin", "bogus", "")
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("error closing /login response body: %v", err)
		}
	}()
	got := resp.StatusCode
	expected := http.StatusUnauthorized
	if got != expected {
		t.Fatalf("invalid status code received. expected %d got %d", expected, got)
	}
}

func TestSuccessfulLogin(t *testing.T) {
	ctx := setupTest(t)
	defer tearDown(t, ctx)
	resp := attemptLogin(t, ctx, nil, "admin", "gophish", "")
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("error closing /login response body: %v", err)
		}
	}()
	got := resp.StatusCode
	expected := http.StatusOK
	if got != expected {
		t.Fatalf("invalid status code received. expected %d got %d", expected, got)
	}
}

func TestInitialAdministratorMustChangePassword(t *testing.T) {
	ctx := setupTest(t)
	defer tearDown(t, ctx)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}

	loginResponse := attemptLogin(t, ctx, client, "admin", "gophish", "")
	defer func() { _ = loginResponse.Body.Close() }()
	if loginResponse.Request.URL.Path != "/reset_password" {
		t.Fatalf("initial login was not redirected to password reset")
	}

	changedPassword := "synthetic-reset-value"
	request, err := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf("%s/reset_password", ctx.adminServer.URL),
		strings.NewReader(url.Values{
			"password":         {changedPassword},
			"confirm_password": {changedPassword},
		}.Encode()),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()

	admin, err := models.GetUserByUsername(models.DefaultAdminUsername)
	if err != nil {
		t.Fatal(err)
	}
	if admin.PasswordChangeRequired {
		t.Fatal("successful initial password reset did not clear forced-change marker")
	}
	if err := auth.ValidatePassword(changedPassword, admin.Hash); err != nil {
		t.Fatal("successful initial password reset did not store the new password")
	}
}

func TestSuccessfulRedirect(t *testing.T) {
	ctx := setupTest(t)
	defer tearDown(t, ctx)
	next := "/campaigns"
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	resp := attemptLogin(t, ctx, client, "admin", "gophish", fmt.Sprintf("?next=%s", next))
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("error closing /login response body: %v", err)
		}
	}()
	got := resp.StatusCode
	expected := http.StatusFound
	if got != expected {
		t.Fatalf("invalid status code received. expected %d got %d", expected, got)
	}
	url, err := resp.Location()
	if err != nil {
		t.Fatalf("error parsing response Location header: %v", err)
	}
	if url.Path != next {
		t.Fatalf("unexpected Location header received. expected %s got %s", next, url.Path)
	}
}

func TestAccountLocked(t *testing.T) {
	ctx := setupTest(t)
	defer tearDown(t, ctx)
	resp := attemptLogin(t, ctx, nil, "houdini", "gophish", "")
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("error closing /login response body: %v", err)
		}
	}()
	got := resp.StatusCode
	expected := http.StatusUnauthorized
	if got != expected {
		t.Fatalf("invalid status code received. expected %d got %d", expected, got)
	}
}
