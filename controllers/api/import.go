package api

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/Vesperis-group/gophishfr/dialer"
	log "github.com/Vesperis-group/gophishfr/logger"
	"github.com/Vesperis-group/gophishfr/models"
	"github.com/Vesperis-group/gophishfr/util"
	"github.com/jordan-wright/email"
)

type cloneRequest struct {
	URL              string `json:"url"`
	IncludeResources bool   `json:"include_resources"`
}

func (cr *cloneRequest) validate() error {
	if cr.URL == "" {
		return errors.New("No URL Specified")
	}
	return nil
}

type cloneResponse struct {
	HTML string `json:"html"`
}

type emailResponse struct {
	Text    string `json:"text"`
	HTML    string `json:"html"`
	Subject string `json:"subject"`
}

// ImportGroup imports a CSV of group members
func (as *Server) ImportGroup(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		JSONResponse(w, models.Response{Success: false, Message: "Method not allowed"}, http.StatusBadRequest)
		return
	}
	if !isMultipartFormData(r.Header.Get("Content-Type")) {
		JSONResponse(w, models.Response{Success: false, Message: "Expected a multipart/form-data upload"}, http.StatusUnsupportedMediaType)
		return
	}
	// Bound the body before it is parsed so an oversized upload is rejected
	// while it streams instead of being buffered.
	r.Body = http.MaxBytesReader(w, r.Body, util.MaxImportRequestBytes)
	ts, err := util.ParseCSV(r)
	if err != nil {
		status, message := importGroupError(err)
		JSONResponse(w, models.Response{Success: false, Message: message}, status)
		return
	}
	JSONResponse(w, ts, http.StatusOK)
}

// isMultipartFormData reports whether the declared media type is the multipart
// upload this endpoint accepts. The extension of the uploaded file is metadata
// only and is never used as a control.
func isMultipartFormData(contentType string) bool {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	if mediaType != "multipart/form-data" {
		return false
	}
	return params["boundary"] != ""
}

// importGroupError maps an import failure onto a status code and a message that
// describes the limit without echoing any uploaded content.
func importGroupError(err error) (int, string) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return http.StatusRequestEntityTooLarge, fmt.Sprintf("The uploaded data is larger than %d bytes", util.MaxImportRequestBytes)
	}
	switch {
	case errors.Is(err, util.ErrCSVTooManyParts),
		errors.Is(err, util.ErrCSVTooManyFiles),
		errors.Is(err, util.ErrCSVTooManyRecords),
		errors.Is(err, util.ErrCSVTooManyColumns),
		errors.Is(err, util.ErrCSVFieldTooLong),
		errors.Is(err, util.ErrCSVMalformed):
		return http.StatusBadRequest, err.Error()
	}
	// Every remaining failure comes from a malformed request body, so the
	// client is told what to fix without exposing the underlying error.
	return http.StatusBadRequest, "Error parsing CSV"
}

// ImportEmail allows for the importing of email.
// Returns a Message object
func (as *Server) ImportEmail(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		JSONResponse(w, models.Response{Success: false, Message: "Method not allowed"}, http.StatusBadRequest)
		return
	}
	ir := struct {
		Content      string `json:"content"`
		ConvertLinks bool   `json:"convert_links"`
	}{}
	err := json.NewDecoder(r.Body).Decode(&ir)
	if err != nil {
		JSONResponse(w, models.Response{Success: false, Message: "Error decoding JSON Request"}, http.StatusBadRequest)
		return
	}
	e, err := email.NewEmailFromReader(strings.NewReader(ir.Content))
	if err != nil {
		log.Error(err)
	}
	// If the user wants to convert links to point to
	// the landing page, let's make it happen by changing up
	// e.HTML
	if ir.ConvertLinks {
		d, err := goquery.NewDocumentFromReader(bytes.NewReader(e.HTML))
		if err != nil {
			JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusBadRequest)
			return
		}
		d.Find("a").Each(func(i int, a *goquery.Selection) {
			a.SetAttr("href", "{{.URL}}")
		})
		h, err := d.Html()
		if err != nil {
			JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusInternalServerError)
			return
		}
		e.HTML = []byte(h)
	}
	er := emailResponse{
		Subject: e.Subject,
		Text:    string(e.Text),
		HTML:    string(e.HTML),
	}
	JSONResponse(w, er, http.StatusOK)
}

// ImportSite allows for the importing of HTML from a website
// Without "include_resources" set, it will merely place a "base" tag
// so that all resources can be loaded relative to the given URL.
func (as *Server) ImportSite(w http.ResponseWriter, r *http.Request) {
	cr := cloneRequest{}
	if r.Method != "POST" {
		JSONResponse(w, models.Response{Success: false, Message: "Method not allowed"}, http.StatusBadRequest)
		return
	}
	err := json.NewDecoder(r.Body).Decode(&cr)
	if err != nil {
		JSONResponse(w, models.Response{Success: false, Message: "Error decoding JSON Request"}, http.StatusBadRequest)
		return
	}
	if err = cr.validate(); err != nil {
		JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusBadRequest)
		return
	}
	restrictedDialer := dialer.Dialer()
	tr := &http.Transport{
		DialContext: restrictedDialer.DialContext,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}
	client := &http.Client{Transport: tr}
	resp, err := client.Get(cr.URL)
	if err != nil {
		JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusBadRequest)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	// Insert the base href tag to better handle relative resources
	d, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusBadRequest)
		return
	}
	// Assuming we don't want to include resources, we'll need a base href
	if d.Find("head base").Length() == 0 {
		d.Find("head").PrependHtml(fmt.Sprintf("<base href=\"%s\">", cr.URL))
	}
	forms := d.Find("form")
	forms.Each(func(i int, f *goquery.Selection) {
		// We'll want to store where we got the form from
		// (the current URL)
		url := f.AttrOr("action", cr.URL)
		if !strings.HasPrefix(url, "http") {
			url = fmt.Sprintf("%s%s", cr.URL, url)
		}
		f.PrependHtml(fmt.Sprintf("<input type=\"hidden\" name=\"__original_url\" value=\"%s\"/>", url))
	})
	h, err := d.Html()
	if err != nil {
		JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusInternalServerError)
		return
	}
	cs := cloneResponse{HTML: h}
	JSONResponse(w, cs, http.StatusOK)
}
