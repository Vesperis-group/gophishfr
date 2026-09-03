package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/mail"

	ctx "github.com/Vesperis-group/gophishfr/context"
	log "github.com/Vesperis-group/gophishfr/logger"
	"github.com/Vesperis-group/gophishfr/models"
	"github.com/jinzhu/gorm"
	"github.com/sirupsen/logrus"
)

type emailRequestAlias models.EmailRequest

type emailRequest struct {
	emailRequestAlias
	SMTP smtpRequest `json:"smtp"`
}

func decodeEmailRequest(r *http.Request) (*models.EmailRequest, error) {
	request := emailRequest{}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		return nil, err
	}
	if bytes.Equal(bytes.TrimSpace(request.SMTP.Password), []byte("null")) {
		return nil, errNullSMTPPassword
	}
	if len(request.SMTP.Password) > 0 {
		if err := json.Unmarshal(request.SMTP.Password, &request.SMTP.SMTP.Password); err != nil {
			return nil, err
		}
	}
	result := models.EmailRequest(request.emailRequestAlias)
	result.SMTP = request.SMTP.SMTP
	return &result, nil
}

// SendTestEmail sends a test email using the template name
// and Target given.
func (as *Server) SendTestEmail(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		JSONResponse(w, models.Response{Success: false, Message: "Method not allowed"}, http.StatusBadRequest)
		return
	}
	s, err := decodeEmailRequest(r)
	if err != nil {
		JSONResponse(w, models.Response{Success: false, Message: "Error decoding JSON Request"}, http.StatusBadRequest)
		return
	}
	s.ErrorChan = make(chan error)
	s.UserId = ctx.Get(r, "user_id").(int64)

	storeRequest := false

	// If a Template is not specified use a default
	if s.Template.Name == "" {
		//default message body
		text := "It works!\n\nThis email confirms that your GophishFR\nconfiguration was successful.\n" +
			"Here are the details:\n\nWho you sent from: {{.From}}\n\nWho you sent to: \n" +
			"{{if .FirstName}} First Name: {{.FirstName}}\n{{end}}" +
			"{{if .LastName}} Last Name: {{.LastName}}\n{{end}}" +
			"{{if .Position}} Position: {{.Position}}\n{{end}}" +
			"\nYou can now launch a security awareness simulation."
		t := models.Template{
			Subject: "Default Email from GophishFR",
			Text:    text,
		}
		s.Template = t
	} else {
		// Get the Template requested by name
		s.Template, err = models.GetTemplateByName(s.Template.Name, s.UserId)
		if err == gorm.ErrRecordNotFound {
			log.WithFields(logrus.Fields{
				"template": s.Template.Name,
			}).Error("Template does not exist")
			JSONResponse(w, models.Response{Success: false, Message: models.ErrTemplateNotFound.Error()}, http.StatusBadRequest)
			return
		} else if err != nil {
			log.Error(err)
			JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusBadRequest)
			return
		}
		s.TemplateId = s.Template.Id
		// We'll only save the test request to the database if there is a
		// user-specified template to use.
		storeRequest = true
	}

	if s.Page.Name != "" {
		s.Page, err = models.GetPageByName(s.Page.Name, s.UserId)
		if err == gorm.ErrRecordNotFound {
			log.WithFields(logrus.Fields{
				"page": s.Page.Name,
			}).Error("Page does not exist")
			JSONResponse(w, models.Response{Success: false, Message: models.ErrPageNotFound.Error()}, http.StatusBadRequest)
			return
		} else if err != nil {
			log.Error(err)
			JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusBadRequest)
			return
		}
		s.PageId = s.Page.Id
	}

	incomingPassword := s.SMTP.Password
	s.SMTP.Password = ""
	if s.SMTP.Id != 0 {
		stored, lookupErr := models.GetSMTP(s.SMTP.Id, s.UserId)
		if lookupErr != nil {
			JSONResponse(w, models.Response{Success: false, Message: "Sending profile not found"}, http.StatusBadRequest)
			return
		}
		if incomingPassword == "" {
			s.SMTP.PasswordCiphertext = stored.PasswordCiphertext
			s.SMTP.UserId = stored.UserId
		}
	}
	// If a complete sending profile is provided use it.
	if err := s.SMTP.Validate(); err != nil {
		// Otherwise get the SMTP requested by name
		smtp, lookupErr := models.GetSMTPByName(s.SMTP.Name, s.UserId)
		// If the Sending Profile doesn't exist, let's err on the side
		// of caution and assume that the validation failure was more important.
		if lookupErr != nil {
			log.Error(err)
			JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusBadRequest)
			return
		}
		s.SMTP = smtp
	}
	if incomingPassword != "" {
		s.SetRuntimeSMTPPassword(incomingPassword)
		s.SMTP.Password = ""
		s.SMTP.PasswordCiphertext = ""
	}

	_, err = mail.ParseAddress(s.Template.EnvelopeSender)
	if err != nil {
		_, err = mail.ParseAddress(s.SMTP.FromAddress)
		if err != nil {
			JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusInternalServerError)
			return
		} else {
			s.FromAddress = s.SMTP.FromAddress
		}
	} else {
		s.FromAddress = s.Template.EnvelopeSender
	}

	// Validate the given request
	if err = s.Validate(); err != nil {
		JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusBadRequest)
		return
	}

	// Store the request if this wasn't the default template
	if storeRequest {
		err = models.PostEmailRequest(s)
		if err != nil {
			log.Error(err)
			JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusInternalServerError)
			return
		}
	}
	// Send the test email
	err = as.worker.SendTestEmail(s)
	if err != nil {
		log.Error(err)
		JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusInternalServerError)
		return
	}
	JSONResponse(w, models.Response{Success: true, Message: "Email Sent"}, http.StatusOK)
}
