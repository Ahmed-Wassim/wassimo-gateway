package main

import (
	"log"
	"net/http"

	"github.com/ahmed-wassim/wassimo-gateway/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	if err := http.ListenAndServe(":"+cfg.PORT, mux); err != nil {
		log.Fatal("server is down ", err)
	}
}
