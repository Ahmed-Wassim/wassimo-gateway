package jwt

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

func LoadPublicKey(encoded string) (ed25519.PublicKey, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}

	return ed25519.PublicKey(raw), nil
}

func Bearer(token string) (string, error) {
	if !strings.HasPrefix(token, "Bearer ") {
		return "", fmt.Errorf("invalid bearer token: %s", token)
	}
	return strings.TrimPrefix(token, "Bearer "), nil
}

func Split(token string) (headerB64, payloadB64, signingInput string, signature []byte, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", "", "", nil, fmt.Errorf("invalid token: %s", token)
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", "", "", nil, fmt.Errorf("decode signature: %w", err)
	}

	return parts[0], parts[1], parts[0] + "." + parts[1], sig, nil
}

func CheckHeader(headerB64, wantKid string) error {
	raw, err := base64.RawURLEncoding.DecodeString(headerB64)
	if err != nil {
		return fmt.Errorf("decode header: %w", err)
	}
	var h struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return fmt.Errorf("parse header: %w", err)
	}
	if h.Alg != "EdDSA" {
		return fmt.Errorf("unexpected alg %q", h.Alg)
	}
	if wantKid != "" && h.Kid != wantKid {
		return fmt.Errorf("unexpected kid %q", h.Kid)
	}
	return nil
}

func VerifySignature(pub ed25519.PublicKey, signingInput string, signature []byte) error {
	if !ed25519.Verify(pub, []byte(signingInput), signature) {
		return fmt.Errorf("bad signature")
	}
	return nil
}

type Claims struct {
	Sub         string   `json:"sub"`
	Device      string   `json:"device"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
	Exp         int64    `json:"exp"`
	Iss         string   `json:"iss"`
}

func DecodeClaims(payloadB64, wantIss string, wantAud []string, leeway time.Duration) (Claims, error) {
	var c Claims
	raw, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return c, fmt.Errorf("decode payload: %w", err)
	}
	var wire struct {
		Claims
		Aud any `json:"aud"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return c, fmt.Errorf("parse payload: %w", err)
	}
	if time.Now().Add(leeway).Unix() > wire.Exp {
		return c, fmt.Errorf("token expired")
	}
	if wire.Iss != wantIss {
		return c, fmt.Errorf("wrong issuer")
	}
	audOK := false
	switch a := wire.Aud.(type) {
	case string:
		audOK = slices.Contains(wantAud, a)
	case []any:
		for _, v := range a {
			if s, ok := v.(string); ok && slices.Contains(wantAud, s) {
				audOK = true
				break
			}
		}
	}
	if !audOK {
		return c, fmt.Errorf("wrong audience")
	}
	return wire.Claims, nil
}

type VerifierConfig struct {
	Issuer   string
	Audience []string
	Kid      string
	Leeway   time.Duration
}

func VerifyToken(pub ed25519.PublicKey, cfg VerifierConfig, token string) (Claims, error) {
	var c Claims
	h64, p64, input, sig, err := Split(token)
	if err != nil {
		return c, err
	}
	if err := CheckHeader(h64, cfg.Kid); err != nil {
		return c, err
	}
	if err := VerifySignature(pub, input, sig); err != nil {
		return c, err
	}
	return DecodeClaims(p64, cfg.Issuer, cfg.Audience, cfg.Leeway)
}
