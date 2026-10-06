package handlers

import (
	"io"
	"net/http"

	"github.com/ahmed-wassim/wassimo-gateway/internal/helpers"
)

// Handler holds the upstream base URLs and the shared HTTP client.
// New service = new *Base field + routes in main.go.
type Handler struct {
	CatalogBase  string
	IdentityBase string
	CartBase     string
	Client       *http.Client
}

// Keep-path proxy: upstream URL is base + path, so main.go owns routing.
func (h *Handler) Proxy(w http.ResponseWriter, r *http.Request) {
	h.proxyTo(w, r, h.CatalogBase, false)
}

func (h *Handler) ProxyIdentity(w http.ResponseWriter, r *http.Request) {
	h.proxyTo(w, r, h.IdentityBase, false)
}

func (h *Handler) ProxyCart(w http.ResponseWriter, r *http.Request) {
	h.proxyTo(w, r, h.CartBase, true)
}

func (h *Handler) proxyTo(w http.ResponseWriter, r *http.Request, base string, forwardUser bool) {
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

	// Identity authenticates the Bearer itself; stripping it would 401 every
	// protected call. Accept survives so errors come back JSON, not HTML.
	if auth := r.Header.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}

	if accept := r.Header.Get("Accept"); accept != "" {
		req.Header.Set("Accept", accept)
	}

	// Downstream owner headers — cart ONLY. RequireAuth already stripped any
	// client-sent values and Set verified ones, so forwarding here is safe.
	// Identity and catalog never receive these (their routes pass false).
	if forwardUser {
		for _, k := range []string{"X-User-Id", "X-User-Roles", "X-User-Permissions"} {
			if v := r.Header.Get(k); v != "" {
				req.Header.Set(k, v)
			}
		}
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
