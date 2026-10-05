package handlers

import (
	"io"
	"net/http"

	"github.com/ahmed-wassim/wassimo-gateway/internal/helpers"
)

// Handler holds the upstream base URLs and the shared HTTP client.
// Adding a new service is: add a *Base field + register routes in main.go.
// The Proxy function itself never changes.
type Handler struct {
	CatalogBase  string
	IdentityBase string
	Client       *http.Client
}

// Proxy forwards the request to the correct upstream using the base URL
// embedded in the handler the route was registered on.
//
// Keep-path design: the upstream URL is base + r.URL.Path, so route
// registration in main.go determines which service receives the request
// without any path rewriting here.
func (h *Handler) Proxy(w http.ResponseWriter, r *http.Request) {
	h.proxyTo(w, r, h.CatalogBase)
}

// ProxyIdentity forwards to the identity upstream.
// Split from Proxy so the auth middleware can target identity specifically
// without embedding the URL in the middleware itself.
func (h *Handler) ProxyIdentity(w http.ResponseWriter, r *http.Request) {
	h.proxyTo(w, r, h.IdentityBase)
}

func (h *Handler) proxyTo(w http.ResponseWriter, r *http.Request, base string) {
	url := base + r.URL.Path
	if r.URL.RawQuery != "" {
		url += "?" + r.URL.RawQuery
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, url, r.Body)
	if err != nil {
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}

	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}

	// Forward the caller's auth and content negotiation. Identity
	// authenticates the Bearer token itself on protected routes — stripping
	// Authorization here would turn every gatewayed protected call into a
	// 401 from identity even after RequireAuth already accepted it.
	// Accept must survive so validation errors come back as JSON, not HTML.
	if auth := r.Header.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}

	if accept := r.Header.Get("Accept"); accept != "" {
		req.Header.Set("Accept", accept)
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
