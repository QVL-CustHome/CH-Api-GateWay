package main

import (
	"log"
	"net/http"

	"github.com/custhome/ch-api-gateway/internal/health"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health.Handler)

	addr := ":8080"
	log.Printf("API Gateway listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
