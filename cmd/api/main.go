package main

import (
	"log"
	"net/http"
	"time"

	"github.com/ahmed-wassim/wassimo-gateway/internal/config"
	"github.com/ahmed-wassim/wassimo-gateway/internal/handlers"
	"github.com/ahmed-wassim/wassimo-gateway/internal/helpers"
	"github.com/google/uuid"
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
	mux.HandleFunc("/restaurants", h.Proxy)
	mux.HandleFunc("/restaurants/", h.Proxy)

	if err := http.ListenAndServe(":"+cfg.PORT, requestID(mux)); err != nil {
		log.Fatal("server is down ", err)
	}
}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(helpers.WithID(r.Context(), id)))
	})
}
