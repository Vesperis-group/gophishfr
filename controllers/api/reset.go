package api

import (
	"errors"
	"net/http"

	ctx "github.com/Vesperis-group/gophishfr/context"
	"github.com/Vesperis-group/gophishfr/models"
)

// Reset (/api/reset) resets the currently authenticated user's API key
func (as *Server) Reset(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == "POST":
		u := ctx.Get(r, "user").(models.User)
		apiKey, err := models.ResetUserAPIKey(u.Id)
		if err != nil {
			status := http.StatusInternalServerError
			message := "Unable to reset API key"
			if errors.Is(err, models.ErrAPIKeyVerifierUnavailable) {
				status = http.StatusServiceUnavailable
				message = "API verifier key unavailable"
			}
			JSONResponse(w, models.Response{Success: false, Message: message}, status)
		} else {
			JSONResponse(w, models.Response{
				Success: true,
				Message: "API Key successfully reset!",
				Data:    apiKey,
			}, http.StatusOK)
		}
	}
}
