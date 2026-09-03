package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	log "github.com/Vesperis-group/gophishfr/logger"
	"github.com/Vesperis-group/gophishfr/models"
	"github.com/Vesperis-group/gophishfr/webhook"
	"github.com/gorilla/mux"
)

// webhookRequest decodes a webhook create/update request body. Secret is a
// RawMessage rather than a plain string so the tri-state intent described in
// models.WebhookSecretIntent can be recovered: a key that is entirely absent
// leaves the RawMessage nil (len 0), which is indistinguishable at this layer
// from JSON null and is handled identically -- see webhookSecretIntentFromRaw.
type webhookRequest struct {
	models.Webhook
	Secret json.RawMessage `json:"secret"`
}

// webhookResponse is the only shape ever written to an HTTP response for a
// webhook. It has no Secret or SecretCiphertext field, so a future field
// added to models.Webhook cannot leak through this response by accident.
type webhookResponse struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	IsActive bool   `json:"is_active"`
}

func newWebhookResponse(wh models.Webhook) webhookResponse {
	return webhookResponse{
		ID:       wh.Id,
		Name:     wh.Name,
		URL:      wh.URL,
		IsActive: wh.IsActive,
	}
}

func newWebhookResponses(whs []models.Webhook) []webhookResponse {
	responses := make([]webhookResponse, len(whs))
	for index, wh := range whs {
		responses[index] = newWebhookResponse(wh)
	}
	return responses
}

// webhookSecretIntentFromRaw classifies a "secret" JSON field into the
// tri-state models.WebhookSecretIntent that both the create/update DTO and
// the validate/ping DTO share: an absent field or JSON null preserves any
// existing secret, an explicitly present empty string clears it, and any
// other string value replaces it.
func webhookSecretIntentFromRaw(raw json.RawMessage) (models.WebhookSecretIntent, string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return models.WebhookSecretPreserve, "", nil
	}
	var secret string
	if err := json.Unmarshal(raw, &secret); err != nil {
		return models.WebhookSecretPreserve, "", err
	}
	if secret == "" {
		return models.WebhookSecretClear, "", nil
	}
	return models.WebhookSecretReplace, secret, nil
}

// decodeWebhookRequest decodes a create/update request body, separating the
// persisted-shape fields (name, URL, is_active) from the tri-state secret
// intent. It never returns a models.Webhook with a non-empty Secret or
// SecretCiphertext: those columns are populated only by the model layer.
func decodeWebhookRequest(body io.Reader) (models.Webhook, models.WebhookSecretIntent, string, error) {
	request := webhookRequest{}
	if err := json.NewDecoder(body).Decode(&request); err != nil {
		return models.Webhook{}, models.WebhookSecretPreserve, "", err
	}
	intent, secret, err := webhookSecretIntentFromRaw(request.Secret)
	if err != nil {
		return models.Webhook{}, models.WebhookSecretPreserve, "", err
	}
	return request.Webhook, intent, secret, nil
}

// decodeWebhookSecretOverride decodes the optional inline secret accepted by
// the validate/ping endpoint. A missing or entirely empty body -- what the
// stock UI's "ping" action sends -- is treated the same as an absent "secret"
// field: use the webhook's own authorized stored secret.
func decodeWebhookSecretOverride(body io.Reader) (models.WebhookSecretIntent, string, error) {
	var request struct {
		Secret json.RawMessage `json:"secret"`
	}
	if err := json.NewDecoder(body).Decode(&request); err != nil {
		if errors.Is(err, io.EOF) {
			return models.WebhookSecretPreserve, "", nil
		}
		return models.WebhookSecretPreserve, "", err
	}
	return webhookSecretIntentFromRaw(request.Secret)
}

// isInvalidWebhookRequest reports whether err reflects a problem with the
// caller's input (missing name/URL, an oversized or non-UTF-8 secret, or an
// otherwise malformed secret request) rather than a server-side or
// credential-availability problem.
func isInvalidWebhookRequest(err error) bool {
	return errors.Is(err, models.ErrURLNotSpecified) ||
		errors.Is(err, models.ErrNameNotSpecified) ||
		errors.Is(err, models.ErrWebhookCredentialTooLong) ||
		errors.Is(err, models.ErrWebhookCredentialInvalidEncoding) ||
		errors.Is(err, models.ErrWebhookCredentialInvalidState)
}

func webhookMutationStatus(err error) int {
	if isInvalidWebhookRequest(err) {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// Webhooks returns a list of webhooks, both active and disabled
func (as *Server) Webhooks(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == "GET":
		whs, err := models.GetWebhooks()
		if err != nil {
			log.Error(err)
			JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusInternalServerError)
			return
		}
		JSONResponse(w, newWebhookResponses(whs), http.StatusOK)

	case r.Method == "POST":
		wh, intent, secret, err := decodeWebhookRequest(r.Body)
		if err != nil {
			JSONResponse(w, models.Response{Success: false, Message: "Invalid JSON structure"}, http.StatusBadRequest)
			return
		}
		err = models.PostWebhook(&wh, intent, secret, as.credentialCipher)
		if err != nil {
			JSONResponse(w, models.Response{Success: false, Message: err.Error()}, webhookMutationStatus(err))
			return
		}
		JSONResponse(w, newWebhookResponse(wh), http.StatusCreated)
	}
}

// Webhook returns details of a single webhook specified by "id" parameter
func (as *Server) Webhook(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, _ := strconv.ParseInt(vars["id"], 0, 64)
	wh, err := models.GetWebhook(id)
	if err != nil {
		JSONResponse(w, models.Response{Success: false, Message: "Webhook not found"}, http.StatusNotFound)
		return
	}
	switch {
	case r.Method == "GET":
		JSONResponse(w, newWebhookResponse(wh), http.StatusOK)

	case r.Method == "DELETE":
		err = models.DeleteWebhook(id)
		if err != nil {
			JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusInternalServerError)
			return
		}
		log.Infof("Deleted webhook with id: %d", id)
		JSONResponse(w, models.Response{Success: true, Message: "Webhook deleted Successfully!"}, http.StatusOK)

	case r.Method == "PUT":
		updated, intent, secret, err := decodeWebhookRequest(r.Body)
		if err != nil {
			log.Errorf("error decoding webhook: %v", err)
			JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusBadRequest)
			return
		}
		updated.Id = id
		err = models.PutWebhook(&updated, intent, secret, as.credentialCipher)
		if err != nil {
			JSONResponse(w, models.Response{Success: false, Message: err.Error()}, webhookMutationStatus(err))
			return
		}
		JSONResponse(w, newWebhookResponse(updated), http.StatusOK)
	}
}

// ValidateWebhook makes an HTTP request to a specified remote url to ensure that it's valid.
//
// The optional request body may carry an inline "secret" override with the
// same tri-state semantics as create/update: absent or null uses the
// webhook's own authorized stored secret (decrypted here, never echoed),
// an explicitly present empty string validates the historical no-secret
// signing behavior, and a non-empty string is used only in memory for this
// one request -- it is never persisted or returned. A stored secret that
// cannot be authenticated (unknown key, tamper, wrong record) fails here,
// before webhook.Send is ever called, so no outbound HTTP request is made.
func (as *Server) ValidateWebhook(w http.ResponseWriter, r *http.Request) {
	type validationEvent struct {
		Success bool `json:"success"`
	}
	switch {
	case r.Method == "POST":
		vars := mux.Vars(r)
		id, _ := strconv.ParseInt(vars["id"], 0, 64)
		wh, err := models.GetWebhook(id)
		if err != nil {
			log.Error(err)
			JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusInternalServerError)
			return
		}
		intent, inlineSecret, err := decodeWebhookSecretOverride(r.Body)
		if err != nil {
			JSONResponse(w, models.Response{Success: false, Message: "Invalid request"}, http.StatusBadRequest)
			return
		}
		var secret string
		switch intent {
		case models.WebhookSecretPreserve:
			secret, err = models.DecryptWebhookSecret(wh, as.credentialCipher)
			if err != nil {
				JSONResponse(w, models.Response{Success: false, Message: "Webhook credential unavailable"}, http.StatusBadRequest)
				return
			}
		case models.WebhookSecretClear:
			secret = ""
		case models.WebhookSecretReplace:
			if err := models.ValidateWebhookSecret(inlineSecret); err != nil {
				JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusBadRequest)
				return
			}
			secret = inlineSecret
		}
		payload := validationEvent{Success: true}
		err = webhook.Send(webhook.EndPoint{URL: wh.URL, Secret: secret}, payload)
		if err != nil {
			JSONResponse(w, models.Response{Success: false, Message: err.Error()}, http.StatusBadRequest)
			return
		}
		JSONResponse(w, newWebhookResponse(wh), http.StatusOK)
	}
}
