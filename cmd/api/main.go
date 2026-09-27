package main

import (
	"log"
	"net/http"
	"time"

	"github.com/ahmed-wassim/wassimo-gateway/internal/config"
	"github.com/ahmed-wassim/wassimo-gateway/internal/handlers"
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

	h := &handlers.Handler{CatalogBase: cfg.CATALOG_SERVICE_URL, Client: &http.Client{Timeout: 5 * time.Second}}
	mux.HandleFunc("/restaurants", h.Restaurants)

	if err := http.ListenAndServe(":"+cfg.PORT, mux); err != nil {
		log.Fatal("server is down ", err)
	}
}
