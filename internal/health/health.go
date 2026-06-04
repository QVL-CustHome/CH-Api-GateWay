// Package health expose le endpoint de liveness du gateway.
package health

import (
	"encoding/json"
	"net/http"
)

// Status représente l'état de santé du service.
type Status struct {
	Status string `json:"status"`
}

// Check retourne l'état de santé courant du gateway.
func Check() Status {
	return Status{Status: "ok"}
}

// Handler répond au endpoint GET /health.
func Handler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Check())
}
