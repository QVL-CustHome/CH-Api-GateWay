package health

import (
	"encoding/json"
	"net/http"
)

type Status struct {
	Status string `json:"status"`
}

func Check() Status {
	return Status{Status: "ok"}
}

func Handler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Check())
}
