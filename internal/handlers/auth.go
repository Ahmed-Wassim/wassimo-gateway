package handlers

import (
	"crypto/ed25519"
	"net/http"
	"strings"

	"github.com/ahmed-wassim/wassimo-gateway/internal/jwt"
)

func RequireAuth(pub ed25519.PublicKey, vcfg jwt.VerifierConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Header.Del("X-User-Id")
			r.Header.Del("X-User-Roles")
			r.Header.Del("X-User-Permissions")

			token, err := jwt.Bearer(r.Header.Get("Authorization"))
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, `{"error":"missing or invalid authorization header"}`)
				return
			}
			claims, err := jwt.VerifyToken(pub, vcfg, token)
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, `{"error":"invalid or expired token"}`)
				return
			}
			r.Header.Set("X-User-Id", claims.Sub)
			r.Header.Set("X-User-Roles", strings.Join(claims.Roles, ","))
			r.Header.Set("X-User-Permissions", strings.Join(claims.Permissions, ","))

			next.ServeHTTP(w, r)
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write([]byte(body))
}
