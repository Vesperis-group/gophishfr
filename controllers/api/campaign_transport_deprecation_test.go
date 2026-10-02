package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Vesperis-group/gophishfr/models"
)

// campaignTransportFixture creates one real, independently completable
// campaign (group, template, landing page, sending profile, and the
// campaign itself) owned by the admin test user and returns its ID. A
// future launch date keeps the new campaign CampaignQueued rather than
// CampaignInProgress, matching an operator completing a scheduled campaign
// early through the API -- the exact path this test exercises.
func campaignTransportFixture(t *testing.T, name string) int64 {
	t.Helper()

	group := models.Group{Name: name + "-group"}
	group.UserId = 1
	group.Targets = []models.Target{
		{BaseRecipient: models.BaseRecipient{Email: "test1@example.com", FirstName: "First", LastName: "Example"}},
	}
	if err := models.PostGroup(&group); err != nil {
		t.Fatalf("create group: %v", err)
	}

	tmpl := models.Template{Name: name + "-template"}
	tmpl.Subject = "{{.RId}} - Subject"
	tmpl.Text = "{{.RId}} - Text"
	tmpl.UserId = 1
	if err := models.PostTemplate(&tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}

	page := models.Page{Name: name + "-page"}
	page.HTML = "<html>Test</html>"
	page.UserId = 1
	if err := models.PostPage(&page); err != nil {
		t.Fatalf("create page: %v", err)
	}

	smtp := models.SMTP{Name: name + "-smtp"}
	smtp.UserId = 1
	smtp.Host = "example.com"
	smtp.FromAddress = "test@test.com"
	if err := models.PostSMTP(&smtp); err != nil {
		t.Fatalf("create sending profile: %v", err)
	}

	campaign := models.Campaign{Name: name}
	campaign.UserId = 1
	campaign.Template = tmpl
	campaign.Page = page
	campaign.SMTP = smtp
	campaign.Groups = []models.Group{group}
	campaign.LaunchDate = time.Now().UTC().Add(24 * time.Hour)
	if err := models.PostCampaign(&campaign, 1); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	return campaign.Id
}

// assertCampaignCompleted dispatches request through the real API router
// (RequireAPIKey middleware, mux route table, and the CampaignComplete
// handler -- nothing is bypassed) and asserts it both authenticated and
// performed the real business operation: the targeted campaign is reloaded
// from the database and must show CampaignComplete, not just a 200 with an
// empty body.
func assertCampaignCompleted(t *testing.T, server *Server, campaignID int64, request *http.Request) {
	t.Helper()
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s %s: expected 200 OK, got %d: %s",
			request.Method, request.URL.String(), recorder.Code, recorder.Body.String())
	}
	var response models.Response
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response body %q: %v", recorder.Body.String(), err)
	}
	if !response.Success {
		t.Fatalf("expected a successful completion response, got %q", response.Message)
	}
	completed, err := models.GetCampaign(campaignID, 1)
	if err != nil {
		t.Fatalf("reload campaign %d: %v", campaignID, err)
	}
	if completed.Status != models.CampaignComplete {
		t.Fatalf("expected campaign %d status %q after completion, got %q",
			campaignID, models.CampaignComplete, completed.Status)
	}
}

// TestCampaignCompleteTransportDeprecationEquivalence is the executable
// regression required by the deprecation guide
// (docs/API_KEY_TRANSPORT_DEPRECATION.md "Migrate to the canonical
// transport"): every deprecated transport shown there, and its canonical
// Bearer replacement, must authenticate against the real router and
// perform the identical real business operation -- completing
// POST /api/campaigns/{id}/complete, with no trailing slash -- during this
// deprecation release.
func TestCampaignCompleteTransportDeprecationEquivalence(t *testing.T) {
	ctx := setupTest(t)
	completePath := func(id int64) string {
		return fmt.Sprintf("/api/campaigns/%d/complete", id)
	}

	t.Run("query parameter (deprecated) completes the campaign", func(t *testing.T) {
		id := campaignTransportFixture(t, "query-transport-campaign")
		request := httptest.NewRequest(
			http.MethodPost,
			completePath(id)+"?api_key="+url.QueryEscape(ctx.apiKey),
			nil,
		)
		assertCampaignCompleted(t, ctx.apiServer, id, request)
	})

	t.Run("form parameter (deprecated) completes the equivalent campaign", func(t *testing.T) {
		id := campaignTransportFixture(t, "form-transport-campaign")
		request := httptest.NewRequest(
			http.MethodPost,
			completePath(id),
			strings.NewReader(url.Values{"api_key": {ctx.apiKey}}.Encode()),
		)
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		assertCampaignCompleted(t, ctx.apiServer, id, request)
	})

	t.Run("raw Authorization (deprecated) completes the equivalent campaign", func(t *testing.T) {
		id := campaignTransportFixture(t, "raw-transport-campaign")
		request := httptest.NewRequest(http.MethodPost, completePath(id), nil)
		request.Header.Set("Authorization", ctx.apiKey)
		assertCampaignCompleted(t, ctx.apiServer, id, request)
	})

	t.Run("Authorization Bearer (canonical) completes the equivalent campaign", func(t *testing.T) {
		id := campaignTransportFixture(t, "bearer-transport-campaign")
		request := httptest.NewRequest(http.MethodPost, completePath(id), nil)
		request.Header.Set("Authorization", "Bearer "+ctx.apiKey)
		assertCampaignCompleted(t, ctx.apiServer, id, request)
	})

	// Guards the documentation's explicit "no trailing slash" claim: a
	// trailing-slash request to the same path must not silently match the
	// complete route, which is exactly the class of mistake that made the
	// deprecation guide's original form example non-executable.
	t.Run("trailing slash does not match the complete route", func(t *testing.T) {
		id := campaignTransportFixture(t, "trailing-slash-campaign")
		request := httptest.NewRequest(
			http.MethodPost,
			completePath(id)+"/?api_key="+url.QueryEscape(ctx.apiKey),
			nil,
		)
		recorder := httptest.NewRecorder()
		ctx.apiServer.ServeHTTP(recorder, request)
		if recorder.Code == http.StatusOK {
			t.Fatalf("expected the trailing-slash path not to match /complete, got 200: %s", recorder.Body.String())
		}
		completed, err := models.GetCampaign(id, 1)
		if err != nil {
			t.Fatalf("reload campaign %d: %v", id, err)
		}
		if completed.Status == models.CampaignComplete {
			t.Fatalf("trailing-slash request must not have completed campaign %d", id)
		}
	})
}
