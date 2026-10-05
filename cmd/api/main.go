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

	// Shared HTTP client — one per process, not per request.
	// Proxy timeout: 5s (generous for menu queries).
	// Auth timeout: 2s (enforced inside RequireAuth — auth is in front of
	// every protected request so a tight budget is essential).
	client := &http.Client{Timeout: 5 * time.Second}

	h := &handlers.Handler{
		CatalogBase:  cfg.CATALOG_SERVICE_URL,
		IdentityBase: cfg.IDENTITY_SERVICE_URL,
		Client:       client,
	}

	requireAuth := handlers.RequireAuth(cfg.IDENTITY_SERVICE_URL, client)

	mux := http.NewServeMux()

	// ── Health (gateway-own, no upstream) ────────────────────────────────
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	// ── Catalog routes (public) ───────────────────────────────────────────
	mux.HandleFunc("/restaurants", h.Proxy)
	mux.HandleFunc("/restaurants/", h.Proxy)

	// ── Identity: public auth routes ──────────────────────────────────────
	// These must NOT require a token — register/login/forgot are how a user
	// gets a token in the first place.
	mux.HandleFunc("/auth/register",         h.ProxyIdentity)
	mux.HandleFunc("/auth/login",            h.ProxyIdentity)
	mux.HandleFunc("/auth/refresh",          h.ProxyIdentity)

	// ── Identity: protected auth routes ──────────────────────────────────
	// Each route is wrapped individually (not globally) so public catalog
	// browsing keeps working when identity is down. ADR-013 v2.
	mux.Handle("/auth/me",              requireAuth(http.HandlerFunc(h.ProxyIdentity)))
	mux.Handle("/auth/logout",          requireAuth(http.HandlerFunc(h.ProxyIdentity)))
	mux.Handle("/auth/logout-all",      requireAuth(http.HandlerFunc(h.ProxyIdentity)))
	mux.Handle("/auth/tokens",          requireAuth(http.HandlerFunc(h.ProxyIdentity)))
	mux.Handle("/auth/tokens/",         requireAuth(http.HandlerFunc(h.ProxyIdentity)))
	mux.Handle("/auth/password/change", requireAuth(http.HandlerFunc(h.ProxyIdentity)))

	// ── Identity: admin user/role surfaces (protected) ───────────────────
	// Identity enforces the users.* permissions itself; the gateway only
	// proves who the caller is, same as the /auth/* protected routes.
	mux.Handle("/users",       requireAuth(http.HandlerFunc(h.ProxyIdentity)))
	mux.Handle("/users/",      requireAuth(http.HandlerFunc(h.ProxyIdentity)))
	mux.Handle("/roles",       requireAuth(http.HandlerFunc(h.ProxyIdentity)))
	mux.Handle("/permissions", requireAuth(http.HandlerFunc(h.ProxyIdentity)))

	// ── Identity: ready probe ─────────────────────────────────────────────
	// Keep-path would forward /identity/ready upstream, which does not exist.
	// Rewrite to identity's /ready before proxying.
	mux.HandleFunc("/identity/ready", func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = "/ready"
		h.ProxyIdentity(w, r)
	})

	log.Printf("gateway listening on :%s  catalog=%s  identity=%s",
		cfg.PORT, cfg.CATALOG_SERVICE_URL, cfg.IDENTITY_SERVICE_URL)

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
