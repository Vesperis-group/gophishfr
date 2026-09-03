package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	ctx "github.com/Vesperis-group/gophishfr/context"
	log "github.com/Vesperis-group/gophishfr/logger"
	"github.com/Vesperis-group/gophishfr/models"
	"github.com/gorilla/mux"
	"github.com/jinzhu/gorm"
)

var errNullSMTPPassword = errors.New("SMTP password cannot be null")

type smtpRequest struct {
	models.SMTP
	Password json.RawMessage `json:"password"`
}

type smtpResponse struct {
	ID               int64           `json:"id"`
	Interface        string          `json:"interface_type"`
	Name             string          `json:"name"`
	Host             string          `json:"host"`
	Username         string          `json:"username,omitempty"`
	FromAddress      string          `json:"from_address"`
	IgnoreCertErrors bool            `json:"ignore_cert_errors"`
	Headers          []models.Header `json:"headers"`
	ModifiedDate     time.Time       `json:"modified_date"`
}

func decodeSMTPRequest(body io.Reader) (models.SMTP, error) {
	request := smtpRequest{}
	if err := json.NewDecoder(body).Decode(&request); err != nil {
		return models.SMTP{}, err
	}
	if bytes.Equal(bytes.TrimSpace(request.Password), []byte("null")) {
		return models.SMTP{}, errNullSMTPPassword
	}
	if len(request.Password) > 0 {
		if err := json.Unmarshal(request.Password, &request.SMTP.Password); err != nil {
			return models.SMTP{}, err
		}
	}
	return request.SMTP, nil
}

func newSMTPResponse(profile models.SMTP) smtpResponse {
	return smtpResponse{
		ID:               profile.Id,
		Interface:        profile.Interface,
		Name:             profile.Name,
		Host:             profile.Host,
		Username:         profile.Username,
		FromAddress:      profile.FromAddress,
		IgnoreCertErrors: profile.IgnoreCertErrors,
		Headers:          profile.Headers,
		ModifiedDate:     profile.ModifiedDate,
	}
}

// SendingProfiles handles requests for the /api/smtp/ endpoint
func (as *Server) SendingProfiles(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == "GET":
		ss, err := models.GetSMTPs(ctx.Get(r, "user_id").(int64))
		if err != nil {
			log.Error(err)
		}
		response := make([]smtpResponse, len(ss))
		for index, profile := range ss {
			response[index] = newSMTPResponse(profile)
		}
		JSONResponse(w, response, http.StatusOK)
	//POST: Create a new SMTP and return it as JSON
	case r.Method == "POST":
		s, err := decodeSMTPRequest(r.Body)
		if err != nil {
			JSONResponse(w, models.Response{Success: false, Message: "Invalid request"}, http.StatusBadRequest)
			return
		}
		// Check to make sure the name is unique
		_, err = models.GetSMTPByName(s.Name, ctx.Get(r, "user_id").(int64))
		if err != gorm.ErrRecordNotFound {
			JSONResponse(w, models.Response{Success: false, Message: "SMTP name already in use"}, http.StatusConflict)
			log.Error(err)
			return
		}
		s.ModifiedDate = time.Now().UTC()
		s.UserId = ctx.Get(r, "user_id").(int64)
		err = models.PostSMTP(&s, as.credentialCipher)
		if err != nil {
			JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusInternalServerError)
			return
		}
		JSONResponse(w, newSMTPResponse(s), http.StatusCreated)
	}
}

// SendingProfile contains functions to handle the GET'ing, DELETE'ing, and PUT'ing
// of a SMTP object
func (as *Server) SendingProfile(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, _ := strconv.ParseInt(vars["id"], 0, 64)
	s, err := models.GetSMTP(id, ctx.Get(r, "user_id").(int64))
	if err != nil {
		JSONResponse(w, models.Response{Success: false, Message: "SMTP not found"}, http.StatusNotFound)
		return
	}
	switch {
	case r.Method == "GET":
		JSONResponse(w, newSMTPResponse(s), http.StatusOK)
	case r.Method == "DELETE":
		err = models.DeleteSMTP(id, ctx.Get(r, "user_id").(int64))
		if err != nil {
			JSONResponse(w, models.Response{Success: false, Message: "Error deleting SMTP"}, http.StatusInternalServerError)
			return
		}
		JSONResponse(w, models.Response{Success: true, Message: "SMTP Deleted Successfully"}, http.StatusOK)
	case r.Method == "PUT":
		s, err = decodeSMTPRequest(r.Body)
		if err != nil {
			JSONResponse(w, models.Response{Success: false, Message: "Invalid request"}, http.StatusBadRequest)
			return
		}
		if s.Id != id {
			JSONResponse(w, models.Response{Success: false, Message: "/:id and /:smtp_id mismatch"}, http.StatusBadRequest)
			return
		}
		err = s.Validate()
		if err != nil {
			JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusBadRequest)
			return
		}
		s.ModifiedDate = time.Now().UTC()
		s.UserId = ctx.Get(r, "user_id").(int64)
		err = models.PutSMTP(&s, as.credentialCipher)
		if err != nil {
			JSONResponse(w, models.Response{Success: false, Message: "Error updating page"}, http.StatusInternalServerError)
			return
		}
		JSONResponse(w, newSMTPResponse(s), http.StatusOK)
	}
}
