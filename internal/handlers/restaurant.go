package handlers

import (
	"io"
	"net/http"
)

type Handler struct {
	CatalogBase string
	Client      *http.Client
}

func (h *Handler) Restaurants(w http.ResponseWriter, r *http.Request) {
	url := h.CatalogBase + "/restaurants"

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
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
