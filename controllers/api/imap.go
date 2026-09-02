package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	ctx "github.com/Vesperis-group/gophishfr/context"
	"github.com/Vesperis-group/gophishfr/imap"
	"github.com/Vesperis-group/gophishfr/models"
)

var errNullIMAPPassword = errors.New("IMAP password cannot be null")

type imapRequest struct {
	models.IMAP
	// RawMessage distinguishes an omitted or empty password from JSON null.
	Password json.RawMessage `json:"password"`
}

type imapResponse struct {
	Enabled                     bool      `json:"enabled"`
	Host                        string    `json:"host"`
	Port                        uint16    `json:"port,string,omitempty"`
	Username                    string    `json:"username"`
	TLS                         bool      `json:"tls"`
	IgnoreCertErrors            bool      `json:"ignore_cert_errors"`
	Folder                      string    `json:"folder"`
	RestrictDomain              string    `json:"restrict_domain"`
	DeleteReportedCampaignEmail bool      `json:"delete_reported_campaign_email"`
	LastLogin                   time.Time `json:"last_login,omitempty"`
	ModifiedDate                time.Time `json:"modified_date"`
	IMAPFreq                    uint32    `json:"imap_freq,string,omitempty"`
}

func decodeIMAPRequest(body io.Reader) (models.IMAP, error) {
	request := imapRequest{}
	if err := json.NewDecoder(body).Decode(&request); err != nil {
		return models.IMAP{}, err
	}
	if bytes.Equal(bytes.TrimSpace(request.Password), []byte("null")) {
		return models.IMAP{}, errNullIMAPPassword
	}
	if len(request.Password) > 0 {
		if err := json.Unmarshal(request.Password, &request.IMAP.Password); err != nil {
			return models.IMAP{}, err
		}
	}
	return request.IMAP, nil
}

func newIMAPResponse(settings models.IMAP) imapResponse {
	return imapResponse{
		Enabled:                     settings.Enabled,
		Host:                        settings.Host,
		Port:                        settings.Port,
		Username:                    settings.Username,
		TLS:                         settings.TLS,
		IgnoreCertErrors:            settings.IgnoreCertErrors,
		Folder:                      settings.Folder,
		RestrictDomain:              settings.RestrictDomain,
		DeleteReportedCampaignEmail: settings.DeleteReportedCampaignEmail,
		LastLogin:                   settings.LastLogin,
		ModifiedDate:                settings.ModifiedDate,
		IMAPFreq:                    settings.IMAPFreq,
	}
}

// IMAPServerValidate handles requests for the /api/imapserver/validate endpoint
func (as *Server) IMAPServerValidate(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == "GET":
		JSONResponse(w, models.Response{Success: false, Message: "Only POSTs allowed"}, http.StatusBadRequest)
	case r.Method == "POST":
		im := models.IMAP{}
		err := json.NewDecoder(r.Body).Decode(&im)
		if err != nil {
			JSONResponse(w, models.Response{Success: false, Message: "Invalid request"}, http.StatusBadRequest)
			return
		}
		err = imap.Validate(&im)
		if err != nil {
			JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusOK)
			return
		}
		JSONResponse(w, models.Response{Success: true, Message: "Successful login."}, http.StatusCreated)
	}
}

// IMAPServer handles requests for the /api/imapserver/ endpoint
func (as *Server) IMAPServer(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == "GET":
		settings, err := models.GetIMAP(ctx.Get(r, "user_id").(int64))
		if err != nil {
			JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusInternalServerError)
			return
		}
		response := make([]imapResponse, len(settings))
		for index, setting := range settings {
			response[index] = newIMAPResponse(setting)
		}
		JSONResponse(w, response, http.StatusOK)

	// POST: Update database
	case r.Method == "POST":
		im, err := decodeIMAPRequest(r.Body)
		if err != nil {
			JSONResponse(w, models.Response{Success: false, Message: "Invalid data. Please check your IMAP settings."}, http.StatusBadRequest)
			return
		}
		im.ModifiedDate = time.Now().UTC()
		err = models.PostIMAP(&im, ctx.Get(r, "user_id").(int64), as.credentialCipher)
		if err != nil {
			JSONResponse(w, models.Response{Success: false, Message: "Unable to save IMAP settings."}, http.StatusInternalServerError)
			return
		}
		JSONResponse(w, models.Response{Success: true, Message: "Successfully saved IMAP settings."}, http.StatusCreated)
	}
}
