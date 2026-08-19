package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Vesperis-group/gophishfr/models"
	"github.com/Vesperis-group/gophishfr/util"
)

const importGroupPath = "/api/import/group"

// buildGroupImportBody returns a multipart body shaped like the one sent by the
// group import UI, along with its Content-Type header.
func buildGroupImportBody(t *testing.T, files map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	for name, content := range files {
		part, err := writer.CreateFormFile("files[]", name)
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatalf("write form file: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return body, writer.FormDataContentType()
}

func importGroupRequest(t *testing.T, method string, files map[string]string) *http.Request {
	t.Helper()
	body, contentType := buildGroupImportBody(t, files)
	request := httptest.NewRequest(method, importGroupPath, body)
	request.Header.Set("Content-Type", contentType)
	return request
}

func decodeResponseMessage(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	response := &models.Response{}
	if err := json.NewDecoder(recorder.Body).Decode(response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return response.Message
}

const validImportCSV = "First Name,Last Name,Email,Position\nJohn,Doe,john.doe@example.invalid,Analyst\n"

func TestImportGroupAcceptsValidUpload(t *testing.T) {
	ctx := setupTest(t)
	recorder := httptest.NewRecorder()
	ctx.apiServer.ImportGroup(recorder, importGroupRequest(t, http.MethodPost, map[string]string{
		"targets.csv": validImportCSV,
	}))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	targets := []models.Target{}
	if err := json.NewDecoder(recorder.Body).Decode(&targets); err != nil {
		t.Fatalf("decode targets: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("imported %d targets, want 1", len(targets))
	}
	want := models.BaseRecipient{
		FirstName: "John",
		LastName:  "Doe",
		Email:     "john.doe@example.invalid",
		Position:  "Analyst",
	}
	if targets[0].BaseRecipient != want {
		t.Fatalf("target = %+v, want %+v", targets[0].BaseRecipient, want)
	}
}

func TestImportGroupRejectsUnsupportedMethods(t *testing.T) {
	ctx := setupTest(t)
	for _, method := range []string{
		http.MethodGet,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
		http.MethodHead,
	} {
		t.Run(method, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx.apiServer.ImportGroup(recorder, importGroupRequest(t, method, map[string]string{
				"targets.csv": validImportCSV,
			}))

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
			}
			if got := decodeResponseMessage(t, recorder); got != "Method not allowed" {
				t.Fatalf("message = %q, want %q", got, "Method not allowed")
			}
		})
	}
}

func TestImportGroupRejectsUnsupportedContentTypes(t *testing.T) {
	ctx := setupTest(t)
	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{name: "json", contentType: "application/json", body: `{"targets": []}`},
		{name: "text", contentType: "text/plain", body: validImportCSV},
		{name: "empty", contentType: "", body: validImportCSV},
		{name: "multipart without boundary", contentType: "multipart/form-data", body: validImportCSV},
		{name: "unparsable media type", contentType: "multipart/form-data; boundary", body: validImportCSV},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, importGroupPath, strings.NewReader(test.body))
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			} else {
				request.Header.Del("Content-Type")
			}
			recorder := httptest.NewRecorder()
			ctx.apiServer.ImportGroup(recorder, request)

			if recorder.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnsupportedMediaType)
			}
		})
	}
}

func TestImportGroupRejectsOversizedRequest(t *testing.T) {
	ctx := setupTest(t)
	filler := strings.Repeat("a", 1024)
	content := new(strings.Builder)
	content.WriteString("First Name,Last Name,Email\n")
	for content.Len() <= int(util.MaxImportRequestBytes) {
		fmt.Fprintf(content, "John,Doe,john.doe@example.invalid,%s\n", filler)
	}

	recorder := httptest.NewRecorder()
	ctx.apiServer.ImportGroup(recorder, importGroupRequest(t, http.MethodPost, map[string]string{
		"targets.csv": content.String(),
	}))

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusRequestEntityTooLarge)
	}
	if got := decodeResponseMessage(t, recorder); !strings.Contains(got, "larger than") {
		t.Fatalf("message = %q, want it to describe the size limit", got)
	}
}

func TestImportGroupAcceptsRequestUnderTheSizeLimit(t *testing.T) {
	ctx := setupTest(t)
	content := new(strings.Builder)
	content.WriteString("First Name,Last Name,Email\n")
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(content, "User%d,Test,user%d@example.invalid\n", i, i)
	}
	if int64(content.Len()) >= util.MaxImportRequestBytes {
		t.Fatalf("fixture of %d bytes is not below the %d byte limit", content.Len(), util.MaxImportRequestBytes)
	}

	recorder := httptest.NewRecorder()
	ctx.apiServer.ImportGroup(recorder, importGroupRequest(t, http.MethodPost, map[string]string{
		"targets.csv": content.String(),
	}))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	targets := []models.Target{}
	if err := json.NewDecoder(recorder.Body).Decode(&targets); err != nil {
		t.Fatalf("decode targets: %v", err)
	}
	if len(targets) != 20000 {
		t.Fatalf("imported %d targets, want 20000", len(targets))
	}
}

func TestImportGroupRejectsInvalidUploads(t *testing.T) {
	ctx := setupTest(t)
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name:  "malformed csv",
			files: map[string]string{"targets.csv": "First Name,Last Name,Email\n\"John,Doe,john.doe@example.invalid\n"},
			want:  util.ErrCSVMalformed.Error(),
		},
		{
			name:  "field longer than the database column",
			files: map[string]string{"targets.csv": "First Name,Last Name,Email\n" + strings.Repeat("a", util.MaxImportFieldCharacters+1) + ",Doe,john.doe@example.invalid\n"},
			want:  util.ErrCSVFieldTooLong.Error(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx.apiServer.ImportGroup(recorder, importGroupRequest(t, http.MethodPost, test.files))

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
			}
			if got := decodeResponseMessage(t, recorder); got != test.want {
				t.Fatalf("message = %q, want %q", got, test.want)
			}
		})
	}
}

func TestImportGroupRejectsTooManyFiles(t *testing.T) {
	ctx := setupTest(t)
	files := map[string]string{}
	for i := 0; i <= util.MaxImportFiles; i++ {
		files[fmt.Sprintf("targets-%d.csv", i)] = validImportCSV
	}

	recorder := httptest.NewRecorder()
	ctx.apiServer.ImportGroup(recorder, importGroupRequest(t, http.MethodPost, files))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if got := decodeResponseMessage(t, recorder); got != util.ErrCSVTooManyFiles.Error() {
		t.Fatalf("message = %q, want %q", got, util.ErrCSVTooManyFiles.Error())
	}
}

// TestImportGroupErrorsStayOpaque keeps rejection messages free of the uploaded
// content, internal paths, and any parser detail.
func TestImportGroupErrorsStayOpaque(t *testing.T) {
	ctx := setupTest(t)
	secret := "canary-value-that-must-not-be-echoed"
	content := fmt.Sprintf(
		"First Name,Last Name,Email\n\"%s,Doe,john.doe@example.invalid\n",
		strings.Repeat(secret, 4),
	)

	recorder := httptest.NewRecorder()
	ctx.apiServer.ImportGroup(recorder, importGroupRequest(t, http.MethodPost, map[string]string{
		"secret-report.csv": content,
	}))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	message := decodeResponseMessage(t, recorder)
	for _, forbidden := range []string{secret, "secret-report.csv", "goroutine", "/home/", ".go:", "parse error"} {
		if strings.Contains(message, forbidden) {
			t.Fatalf("message %q leaked %q", message, forbidden)
		}
	}
}

// TestImportGroupRequiresAuthentication exercises the full API chain so the
// route keeps the protection applied to every other group operation.
func TestImportGroupRequiresAuthentication(t *testing.T) {
	ctx := setupTest(t)

	body, contentType := buildGroupImportBody(t, map[string]string{"targets.csv": validImportCSV})
	anonymous := httptest.NewRequest(http.MethodPost, importGroupPath, body)
	anonymous.Header.Set("Content-Type", contentType)
	anonymousRecorder := httptest.NewRecorder()
	ctx.apiServer.ServeHTTP(anonymousRecorder, anonymous)
	if anonymousRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want %d", anonymousRecorder.Code, http.StatusUnauthorized)
	}

	body, contentType = buildGroupImportBody(t, map[string]string{"targets.csv": validImportCSV})
	invalid := httptest.NewRequest(http.MethodPost, importGroupPath, body)
	invalid.Header.Set("Content-Type", contentType)
	invalid.Header.Set("Authorization", "Bearer not-a-valid-api-key")
	invalidRecorder := httptest.NewRecorder()
	ctx.apiServer.ServeHTTP(invalidRecorder, invalid)
	if invalidRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("invalid key status = %d, want %d", invalidRecorder.Code, http.StatusUnauthorized)
	}

	body, contentType = buildGroupImportBody(t, map[string]string{"targets.csv": validImportCSV})
	authorized := httptest.NewRequest(http.MethodPost, importGroupPath, body)
	authorized.Header.Set("Content-Type", contentType)
	authorized.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ctx.apiKey))
	authorizedRecorder := httptest.NewRecorder()
	ctx.apiServer.ServeHTTP(authorizedRecorder, authorized)
	if authorizedRecorder.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, want %d", authorizedRecorder.Code, http.StatusOK)
	}
}

// TestImportGroupPersistsThroughGroupCreation checks that an accepted import
// still produces a group the database stores and returns unchanged. The import
// endpoint itself performs no write: it parses and returns targets, which the
// group endpoints then persist.
func TestImportGroupPersistsThroughGroupCreation(t *testing.T) {
	ctx := setupTest(t)
	longName := strings.Repeat("é", util.MaxImportFieldCharacters)
	content := fmt.Sprintf(
		"First Name,Last Name,Email,Position\n%s,Doe,john.doe@example.invalid,Analyst\nJane,Roe,jane.roe@example.invalid,Manager\n",
		longName,
	)

	recorder := httptest.NewRecorder()
	ctx.apiServer.ImportGroup(recorder, importGroupRequest(t, http.MethodPost, map[string]string{
		"targets.csv": content,
	}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	targets := []models.Target{}
	if err := json.NewDecoder(recorder.Body).Decode(&targets); err != nil {
		t.Fatalf("decode targets: %v", err)
	}

	group := models.Group{
		Name:    "Imported group",
		UserId:  ctx.admin.Id,
		Targets: targets,
	}
	if err := models.PostGroup(&group); err != nil {
		t.Fatalf("PostGroup() error = %v", err)
	}

	stored, err := models.GetGroup(group.Id, ctx.admin.Id)
	if err != nil {
		t.Fatalf("GetGroup() error = %v", err)
	}
	if len(stored.Targets) != len(targets) {
		t.Fatalf("stored %d targets, want %d", len(stored.Targets), len(targets))
	}

	addresses := map[string]string{}
	for _, target := range stored.Targets {
		addresses[target.Email] = target.FirstName
	}
	if got := addresses["john.doe@example.invalid"]; got != longName {
		t.Fatalf("stored first name of %d characters, want %d", len([]rune(got)), len([]rune(longName)))
	}
	if _, ok := addresses["jane.roe@example.invalid"]; !ok {
		t.Fatal("second imported target was not stored")
	}
}
