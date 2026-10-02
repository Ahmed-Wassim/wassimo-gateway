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

// authUser is the shape of the user object inside /auth/me responses.
// Only the fields the gateway needs to populate downstream headers.
type authUser struct {
	ID          int64    `json:"id"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
}

type meResponse struct {
	User authUser `json:"user"`
}

// RequireAuth returns middleware that validates the Bearer token by calling
// GET {identityBase}/auth/me. On success it sets X-User-* headers and calls
// next. On failure it returns 401 or 503 depending on the failure mode.
//
// This is the single highest-security function in the gateway:
//   - X-User-* headers MUST be Set (overwrite), never Add (append).
//     Using Add lets a client smuggle their own X-User-Id alongside a valid
//     token and produce a request with two values — some HTTP libraries read
//     the first (the attacker's). See GATEWAY-INTEGRATION.md §4.
//   - Inbound X-User-* headers are stripped unconditionally before any check
//     so they cannot reach downstream services even on unauthenticated paths.
//   - Identity unreachable → 503 {"error":"identity unavailable"}, NOT 401.
//     401 on an outage tells valid-session users to log in again; they can't
//     fix it by doing so, and it looks like an auth bug in the error dashboard.
//   - The 2s timeout is intentionally tighter than the proxy's 5s because
//     auth runs in front of every protected request.
func RequireAuth(identityBase string, client *http.Client) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// ── Step 1: strip all inbound X-User-* headers unconditionally ──
			// Must happen before the token check so a request with a bad token
			// still has its forged headers removed.
			r.Header.Del("X-User-Id")
			r.Header.Del("X-User-Roles")
			r.Header.Del("X-User-Permissions")

			// ── Step 2: extract Bearer token ─────────────────────────────────
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
				writeJSON(w, http.StatusUnauthorized, `{"error":"missing or invalid authorization header"}`)
				return
			}

			// ── Step 3: validate token via identity /auth/me ─────────────────
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()

			meReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
				identityBase+"/auth/me", nil)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, `{"error":"internal"}`)
				return
			}
			meReq.Header.Set("Authorization", authHeader)

			resp, err := client.Do(meReq)
			if err != nil {
				// Network error or timeout — identity is unavailable.
				// Return 503, not 401, to distinguish a dependency failure
				// from an authentication failure.
				writeJSON(w, http.StatusServiceUnavailable, `{"error":"identity unavailable"}`)
				return
			}
			defer resp.Body.Close()

			// ── Step 4: map identity's answer to X-User-* headers ────────────
			if resp.StatusCode != http.StatusOK {
				// Identity rejected the token (401, 403, etc.).
				// Forward the exact status — the client knows what to do.
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

			// ── Step 5: Set (overwrite) the downstream trust headers ──────────
			// NEVER use Add — see function doc above.
			// NEVER forward these to identity itself (the routes that call this
			// middleware should not point to identity's /auth/me).
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
