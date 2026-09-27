package handlers

import (
	"io"
	"net/http"

	"github.com/ahmed-wassim/wassimo-gateway/internal/helpers"
)

type Handler struct {
	CatalogBase string
	Client      *http.Client
}

func (h *Handler) Restaurants(w http.ResponseWriter, r *http.Request) {
	url := h.CatalogBase + "/restaurants"
	if r.URL.RawQuery != "" {
		url += "?" + r.URL.RawQuery
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}

	if id := helpers.FromContext(r.Context()); id != "" {
		req.Header.Set("X-Request-ID", id)
	}

	resp, err := h.Client.Do(req)
	if err != nil {
		http.Error(w, `{"error":"upstream unavailable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
