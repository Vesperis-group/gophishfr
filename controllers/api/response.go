package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	log "github.com/Vesperis-group/gophishfr/logger"
)

// JSONResponse attempts to set the status code, c, and marshal the given interface, d, into a response that
// is written to the given ResponseWriter.
func JSONResponse(w http.ResponseWriter, d interface{}, c int) {
	dj, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		// http.Error has already written a status and a body; continuing would
		// emit a second header (logged by net/http as "superfluous
		// WriteHeader") followed by an empty payload.
		http.Error(w, "Error creating JSON response", http.StatusInternalServerError)
		log.Error(err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(c)
	// Headers are committed: a write failure means the client went away.
	_, _ = fmt.Fprintf(w, "%s", dj)
}
