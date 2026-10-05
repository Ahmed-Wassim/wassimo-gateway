package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Subset of /auth/me used for the downstream headers.
type authUser struct {
	ID          int64    `json:"id"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
}

type meResponse struct {
	User authUser `json:"user"`
}

// RequireAuth validates the Bearer token via identity's /auth/me (2s timeout).
// On success it Sets (never Adds) X-User-* headers, stripping inbound ones
// first so clients cannot spoof identity. Identity down → 503, not 401.
func RequireAuth(identityBase string, client *http.Client) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Strip forged headers before anything else, even with a bad token.
			r.Header.Del("X-User-Id")
			r.Header.Del("X-User-Roles")
			r.Header.Del("X-User-Permissions")

			// Extract the Bearer token.
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
				writeJSON(w, http.StatusUnauthorized, `{"error":"missing or invalid authorization header"}`)
				return
			}

			// Validate the token via identity.
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()

			meReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
				identityBase+"/auth/me", nil)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, `{"error":"internal"}`)
				return
			}
			meReq.Header.Set("Authorization", authHeader)
			// Identity answers JSON only to JSON callers; without Accept it
			// redirects to a login route that does not exist and 500s.
			meReq.Header.Set("Accept", "application/json")

			resp, err := client.Do(meReq)
			if err != nil {
				// Outage is 503, not 401: a valid session must not look expired.
				writeJSON(w, http.StatusServiceUnavailable, `{"error":"identity unavailable"}`)
				return
			}
			defer resp.Body.Close()

			// Forward identity's rejection status as-is.
			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				writeJSONRaw(w, resp.StatusCode, body)
				return
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				writeJSON(w, http.StatusBadGateway, `{"error":"upstream read error"}`)
				return
			}

			var me meResponse
			if err := json.Unmarshal(body, &me); err != nil {
				writeJSON(w, http.StatusBadGateway, `{"error":"upstream parse error"}`)
				return
			}

			// Set, never Add: duplicate headers let clients spoof identity.
			r.Header.Set("X-User-Id", fmt.Sprintf("%d", me.User.ID))
			r.Header.Set("X-User-Roles", strings.Join(me.User.Roles, ","))
			r.Header.Set("X-User-Permissions", strings.Join(me.User.Permissions, ","))

			next.ServeHTTP(w, r)
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write([]byte(body))
}

func writeJSONRaw(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}
