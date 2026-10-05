# Identity ↔ Gateway: full auth cycle, failure guide, and why it is built this way

Audience: anyone who needs to run, debug, extend, or justify the Wassimo
authentication setup — backend devs, reviewers, and non-engineering
stakeholders. The first half is the *what happens*; the second half is the
*why this way and not another way*.

Services involved:

| Piece | Repo / tech | Role in auth |
|---|---|---|
| `wassimo-identity` | Laravel (PHP) on `:9075` → container `identity:8000` | Owns users, passwords, roles/permissions. **Signs** access JWTs, verifies them again on its own routes, owns refresh-token rows. |
| `wassimo-gateway` | Go stdlib `net/http` on `:9051` → container `:8080` | Single public entry point. **Verifies** access JWTs locally with the public key, then proxies to identity/catalog. Owns no users, no passwords, no database. |
| `wassimo-catalog` | Go on `:9050` | Knows nothing about auth (its routes stay public for now). |
| Client (web/mobile) | — | Stores `access_token` (memory) + `refresh_token` (secure storage), sends `Authorization: Bearer …`. |

Contract authority: platform ADR-013 (auth contract, now at v3/v4 behavior:
JWT access + opaque refresh) and ADR-009 (local-vs-Docker URLs are env-only).
If this file and an ADR disagree, the ADR wins and this file must be updated.

---

## 1. The token model (read this first)

There are **two different tokens**, and confusing them is the source of most
misunderstandings:

- **Access token — short-lived Ed25519 JWT (~15 min).** Self-contained:
  header + payload + signature. Identity signs it with the *private* key;
  **anyone with the *public* key can verify it without calling identity**.
  Payload carries `iss`, `aud`, `sub` (user id), `device`, `roles`,
  `permissions`, `iat`, `exp`, `jti`. Sent on every API call.
- **Refresh token — long-lived opaque string (~30 days).** A random
  `id|secret` whose hash lives in identity's `personal_access_tokens` table
  (Laravel Sanctum row named `<device>:refresh`). Meaningless without the
  database. Used **only** on `POST /auth/refresh` to mint a new pair.
  Revocable instantly by deleting the row.

Why two tokens is the whole security story in one paragraph: the token that
travels on every request (theft-prone: logs, proxies, XSS) dies in 15 minutes,
while the powerful long-lived token is used rarely, never leaves secure
storage, and can be killed server-side at any time. A stolen access token
gives at most 15 minutes of access; a stolen refresh token is mitigated by
rotation (each refresh replaces both tokens) and per-device revocation.

`device_name` (optional on login/register, default `"api"`, 1–64 chars) is a
**display label only** — it names the Sanctum row so the session screen
(`GET /auth/tokens`) can show "phone / laptop" instead of five identical rows.
Nothing in the system branches on its value.

---

## 2. Keys: generation, format, distribution

- Algorithm is **EdDSA over Ed25519**, fixed — never negotiated from the
  token header (that negotiation is the classic `alg: none` / HS256-confusion
  hole; both sides hard-code EdDSA).
- Generate with `php artisan jwt:keys` in identity
  (`app/Console/Commands/GenerateJwtKeys.php`). It uses libsodium and writes:
  - `storage/app/jwt/private.key` — base64url of the raw 64-byte secret,
    `0600`, **never committed, never baked into images**;
  - `storage/app/jwt/public.key` — base64url of the raw 32-byte public key.
- No PEM armor, on purpose: PEM invites parsing bugs; the file *is* the
  base64url bytes, nothing else (firebase/php-jwt decodes the last non-empty
  line).
- Distribution: **copy the public key into the gateway's `.env` as
  `JWT_PUBLIC_KEY`** (and `JWT_KID`, see below). The private key never leaves
  identity's volume.
- Identity config (`config/jwt.php`): `issuer = wassimo-identity`,
  `audience = wassimo-gateway`, `kid = ed25519-1`, TTL 15 min, leeway 60 s.
  Gateway config (`internal/config/config.go`): `JWT_PUBLIC_KEY` (required),
  `JWT_ISSUER`, `JWT_AUDIENCE` (comma-separated allowed), `JWT_KID`
  (optional — empty means "accept any kid"), leeway 30 s.
- Rotation: generate with `--force`, copy the new public key to the gateway,
  restart it. All live access tokens die (they are 15-minute tokens; refresh
  pairs re-mint under the new key). Future improvement: overlapping kids
  (accept N−1 and N during a window) — not implemented; single `kid` today.

> Real incident (Oct 2026): the gateway `.env` contained only the first 22 of
> 43 characters of the public key (17 bytes instead of 32). `LoadPublicKey`
> does not length-check, so the gateway booted fine and then rejected **every**
> real token with `invalid or expired token`. Lesson: a key that decodes to
> anything other than 32 bytes is always a copy-paste truncation — verify with
> `python3 -c` byte-length check after every rotation.

---

## 3. The full cycle, request by request

### 3.1 Register — public, straight proxy

```text
Client → POST gateway:9051/auth/register {email, password}
       → gateway ProxyIdentity: base + same path → identity:9075/auth/register
       → 201 {user}  |  409 {error: email already registered}  |  422 validation
```

Gateway does **no validation, no parsing** — it forwards method, path, query,
body, `Content-Type`/`Accept`/`Authorization`, copies status + body back
(`internal/handlers/proxy.go`). No `unique` DB rule on the request (a 422
there would leak enumeration info differently than the contract's 409).

### 3.2 Login — public, identity mints the pair

```text
Client → POST /auth/login {email, password, device_name?}
       → identity AuthService::attemptLogin (constant-time dummy hash on unknown
         email, so unknown vs wrong-password take the same time)
       → 401 {error: invalid credentials}   ← identical for both cases (no oracle)
       → 200 {access_token, refresh_token, token_type, access_token_expires_at,
              refresh_token_expires_at, user}
```

`issueTokenPair` (`AuthService.php:45`): access JWT via `JwtService::issue`
(roles/permissions **baked in** for the gateway's `X-User-*` headers —
they can go stale mid-session; `/auth/me` always reads fresh data), plus a
Sanctum `<device>:refresh` row. Note the baked-in claims are a *hint*;
authorization decisions happen server-side in identity.

### 3.3 Authenticated call — the double-check (the heart of the design)

Example: `GET /auth/me` with `Authorization: Bearer <access JWT>`.

**Hop 1 — gateway `RequireAuth`** (`internal/handlers/auth.go`):
1. Strip any client-supplied `X-User-*` headers (clients must never assert
   identity; the gateway overwrites unconditionally).
2. `jwt.Bearer(...)` — must be exactly `Bearer <token>`, else 401
   `missing or invalid authorization header`.
3. `jwt.VerifyToken(pub, cfg, token)` (`internal/jwt/jwt.go`) — all local,
   no network:
   - `Split`: 3 dot-parts, base64url-decode signature;
   - `CheckHeader`: `alg == "EdDSA"`, and `kid` must match **only if** the
     gateway is configured with one;
   - `VerifySignature`: Ed25519 verify against `JWT_PUBLIC_KEY`;
   - `DecodeClaims`: `exp` within leeway, `iss == wassimo-identity`,
     `aud` matches (string **or** array form — `slices.Contains`).
   - Any failure → 401 `invalid or expired token`. Deliberately one message
     for expired / tampered / wrong-kid / wrong-issuer (see §6).
4. Set `X-User-Id`, `X-User-Roles`, `X-User-Permissions` from claims and
   proxy onward (the `Authorization` header is forwarded untouched).

**Hop 2 — identity `auth.jwt` middleware** (`AuthenticateJwt.php`):
re-verifies the same JWT (signature, `iss`, `aud`), loads the `User` by
`sub`, attaches it + raw claims to the request. Unknown user → 401.
Then the controller runs (`me` returns fresh user + roles/permissions from
the DB, not from the token).

So every protected call is verified **twice, by two different codebases**.
That is intentional — §5 explains why.

### 3.4 Refresh — rotation

```text
Client → POST /auth/refresh {refresh_token}   (public route at gateway)
       → identity looks up the Sanctum row: must exist, have `refresh`
         ability, not be expired → else 401
       → deletes old access+refresh rows, issues a NEW pair (rotation),
         returns it
```

Rotation bounds refresh-token theft: a stolen refresh token is usable at most
until the legitimate client refreshes once, after which the stolen copy is
dead. (Full reuse-detection — "stolen token reused → kill the whole family" —
is a possible hardening, not implemented.)

### 3.5 Logout / logout-all / password change

- `POST /auth/logout` → gateway verifies access JWT (who is calling, which
  device from claims) → identity deletes **that device's** refresh row → 204.
  The access JWT itself stays valid until its 15-minute expiry — this is
  inherent to stateless JWTs, not a bug (§5).
- `POST /auth/logout-all` → deletes **all** refresh rows → 204.
- `POST /auth/password/change {current_password, password}` → wrong current
  password is **422**, not 401, so clients don't discard a good token over a
  typo; other devices' refresh rows are revoked, caller's is kept.
- `GET /auth/tokens` lists refresh rows (`current: true` marks the calling
  device); `DELETE /auth/tokens/:id` revokes one — of *another* user it is
  **403** (must not 404-hide: deleting someone else's token is forbidden,
  not nonexistent).

### 3.6 Catalog (public browsing)

`GET /restaurants…` → gateway `Proxy` → catalog, verbatim, no auth. If
catalog is down: `502 {"error":"upstream unavailable"}` — and critically,
**public browsing never depends on identity** (routes are wrapped per-route,
so an identity outage only breaks protected calls, never the storefront).

### 3.7 Request IDs and timeouts (the glue)

- Gateway's `requestID` middleware mints `X-Request-ID` (uuid) when missing,
  returns it on every response, and propagates it upstream
  (`helpers.WithID` → `proxyTo`). Correlate client ↔ gateway ↔ identity logs
  with this one value.
- Timeouts: shared upstream client 5 s. Upstream failure/timeout → 502.
  Identity's own JWT leeway is 60 s; gateway's is 30 s (clock-skew tolerance,
  not a security boundary).

---

## 4. When something breaks: where to look

### 4.1 Error atlas — who returns what

| Symptom at client | Who produced it | Meaning | Where to look |
|---|---|---|---|
| `401 missing or invalid authorization header` | **gateway** `RequireAuth` | No `Bearer` header | Client code; check header name/scheme |
| `401 invalid or expired token` | **gateway** `VerifyToken` | Expired, bad signature, wrong iss/aud/kid, malformed | Gateway logs + §4.2 checklist |
| `401 invalid credentials` | **identity** login | Unknown email OR wrong password (identical on purpose) | Identity logs; do NOT "fix" by distinguishing |
| `401 unauthenticated` | **identity** middleware | Token failed identity's own verify, or user deleted | Identity logs; compare with gateway verdict |
| `403 …` / `forbidden` | **identity** | Authenticated but not allowed | Roles/permissions in identity DB |
| `409 email already registered` | **identity** | Duplicate register | Expected behavior |
| `422 …` | **identity** | Validation / wrong current password | Request body |
| `502 upstream unavailable` | **gateway** `proxyTo` | Cannot reach identity/catalog (down, DNS, timeout) | `docker ps`, service logs, network |
| Gateway 401 but identity would accept (or vice versa) | key/config drift | `JWT_PUBLIC_KEY` / `ISSUER` / `AUDIENCE` / `KID` mismatch between services | §4.2 |

### 4.2 "Valid token, still 401" checklist (in order)

1. `docker exec wassimo-gateway-gateway-1 printenv JWT_PUBLIC_KEY JWT_KID JWT_ISSUER JWT_AUDIENCE` — is the key the **full 43-char** value from identity's `storage/app/jwt/public.key`? (17/22-char = truncated, the classic.)
2. Decode the JWT payload (`jwt.io` debugger or `base64 -d`): `iss` ==
   `wassimo-identity`? `aud` contains `wassimo-gateway`? `exp` in the future
   (note: identity's `iat`/`exp` are Unix timestamps — container clock skew)?
   Header `kid` == `ed25519-1` (or clear the gateway's `JWT_KID`)?
3. Was the key **rotated** (`jwt:keys --force`) without copying the new
   public key to the gateway and recreating its container? (`env_file` is
   read at container start; editing `.env` alone does nothing until
   `docker compose up -d --build gateway`.)
4. Is the client sending `Authorization: Bearer <token>` exactly —
   `Token`, missing scheme, double `Bearer`, or token in a cookie all fail.
5. Reproduce identity-side: call identity directly (`:9075/auth/me`) with the
   same token. If identity says 200 and gateway says 401, the drift is
   gateway config; if both 401, the token itself is bad/expired.

### 4.3 Useful commands

```bash
# gateway (rebuild + follow logs)
docker compose up -d --build gateway && docker logs -f wassimo-gateway-gateway-1
# key byte-length sanity (must be 32)
python3 -c "import base64;print(len(base64.urlsafe_b64decode(input()+b'='*(-len(input())%4))))"
# full-flow smoke test
curl -s localhost:9051/health && curl -s localhost:9051/identity/ready
```

---

## 5. Why each approach was chosen — with alternatives

### 5.1 Local JWT verification at the gateway (vs remote introspection)

- **Chosen:** gateway verifies the EdDSA signature itself using only the
  public key. Zero extra network hops, ~µs cost, and protected-route latency
  is unaffected by identity load. Identity outages don't cascade: public
  routes keep working, and even protected-route *rejection* of bad tokens
  still works.
- **Alternative — introspection** (`RequireAuth(url, client)` calling
  identity per request, e.g. `POST /auth/me` or `/oauth/introspect`):
  simpler key story (no keys in gateway at all) and instant revocation, but
  every protected call pays a full identity round-trip, identity becomes a
  hard dependency of *all* traffic, and you must add caching — which
  reintroduces staleness and is a worse version of short expiries.
- **Verdict:** local verify wins while access tokens are short-lived (the
  revocation window introspection buys you is already ≤15 min). Revisit only
  if access TTL ever grows past ~1 h.

### 5.2 Double verification (gateway AND identity verify the same token)

Looks redundant; is defense-in-depth with a concrete payoff: the gateway's
check protects *routing* (never forward garbage upstream, never trust
client-supplied `X-User-*`), identity's check protects *data* (user still
exists? fresh roles?). Either side can be misconfigured without the other
silently opening access — a gateway key blunder fails closed at identity,
and identity can additionally enforce per-user revocation the stateless
gateway cannot see. Cost: one signature verify per side (~µs). Alternative
(single verification at gateway, identity trusts `X-User-Id` header) would
turn header spoofing into full impersonation the moment any other caller can
reach identity directly — rejected as too fragile for a network where
services share a Docker bridge.

### 5.3 Ed25519 (vs HS256 vs RS256)

- **HS256 (shared secret):** smallest code, but the verifier holds the
  *signing* secret — any gateway compromise becomes an identity compromise
  (forge arbitrary tokens). Rejected: violates least privilege.
- **RS256 (RSA):** widely supported, but 256-byte signatures bloat every
  request header, key handling is PEM-heavy, and verification is slower.
- **Ed25519:** 64-byte signatures, fast verify, tiny raw keys that fit in env
  without PEM, and libsodium (identity) / stdlib `crypto/ed25519` (gateway)
  support on both stacks. Downside: weaker ecosystem support in very old
  clients — irrelevant here since only our two services verify.

### 5.4 JWT access + opaque refresh (vs all-opaque / all-JWT)

- **All-opaque (ADR-013 v2 era):** every request hits the DB; gateway can
  never verify locally; scales poorly and couples all traffic to identity.
- **All-JWT (incl. refresh as JWT):** refresh becomes unrevocable by design —
  logout-all and per-device revocation become impossible without a denylist,
  which is just the DB again with extra steps.
- **Hybrid (chosen, v3/v4):** each token does what it's good at — stateless
  fast verify for the hot path, server-side rows for the revocable path.
  Price: two code paths and rotation logic. Worth it: it's what makes
  "logout kills sessions" true while keeping per-request auth DB-free.

### 5.5 Short-lived access (15 min) + rotation (vs long-lived tokens)

Bounds every compromise: missed revocation, stolen token, stale
roles/permissions — all self-heal within minutes. Costs: clients must
implement refresh-on-401, and refresh rows need pruning. The 30-day refresh
with rotation keeps UX sane (log in monthly per device, not every 15 min).

### 5.6 "Dumb" keep-path proxy (vs gateway understanding business routes)

`proxyTo` forwards method+path+query and copies status+body; `main.go` owns
the route table, handlers own nothing business-shaped. Adding a service =
one base URL + routes. Pros: gateway rarely changes, can't disagree with
upstream validation, errors stay upstream's (no translation drift). Cons: no
centralized response shaping/validation — accepted because shaping belongs to
the services (gateway must not become a hidden catalog/identity).

### 5.7 One generic 401 (vs detailed "expired / bad-signature / wrong-kid")

Detailed errors are a debugging dream and an attacker's oracle (distinguishing
"expired but correctly signed" from "bad signature" helps forge-and-adapt
attacks, and wrong-issuer detail aids cross-service token replay probing).
So both sides return one 401 message; the *which-check-failed* detail lives
only in server logs. Debug with §4.2, not with client-visible errors.

### 5.8 Keys as files in identity, env string in gateway (vs shared vault / JWKS)

Files avoid PEM-in-env pain and log leakage; env string avoids mounting secret
volumes into the Go container. Alternatives: HashiCorp Vault / JWKS endpoint
(`/.well-known/jwks.json` + kid-based rotation without restarts) — correct
long-term direction once there are multiple gateways or frequent rotation,
but operational overkill for one keypair and one gateway today. Migration
path is clean: serve JWKS from identity, gateway caches by `kid`.

### 5.9 Go stdlib gateway (vs Echo/Gin/Fiber, vs Envoy/Kong/nginx)

Stdlib `ServeMux` + a handful of files: zero framework CVEs, instant builds,
any Go dev can read it in an hour. Frameworks buy routing sugar we don't need
(fewer than 20 routes); full API gateways (Kong/Envoy) buy rate-limiting,
tracing, and plugin ecosystems at the cost of a whole new control plane to
operate. Revisit when the gateway needs rate limiting, retries, or
multi-cluster routing — today it needs forwarding and one middleware.

---

## 6. Business FAQ

- **Why a gateway at all?** One public address, one TLS termination point,
  one place for request IDs/timeouts/CORS, and services stay off the public
  internet. Clients never learn internal topology (`app:8080`,
  `identity:8000`).
- **What does auth cost per request?** One Ed25519 verify (~tens of µs) plus
  the normal proxy hop. No DB, no identity call on the hot path.
- **What happens if identity goes down?** Public catalog browsing: unaffected.
  Login/register/refresh: 502. Protected calls with *invalid* tokens: still
  401 (gateway rejects before dialing). Protected calls with *valid* tokens:
  502 at the proxy step.
- **What does a stolen access token buy?** ≤15 minutes of that user's API
  access, then it dies on its own. Stolen refresh token: usable until the next
  legitimate refresh rotates it away, or the user hits logout-all.
- **Can a logged-out token still be used?** The access JWT, yes, until expiry
  (≤15 min) — the price of statelessness, bounded and documented. The session
  (refresh) dies immediately.
- **Do you store passwords safely?** Identity owns that (bcrypt, `BCRYPT_ROUNDS=12`);
  the gateway never sees a password except as opaque proxied bytes.
- **Compliance posture:** per-device session list + individual/all revocation
  covers the standard "manage my sessions" expectation; `device_name` gives
  users recognizable rows. Audit trail beyond `last_used_at` is future work.

---

## Appendix A — verified flow transcript (Oct 2026, fixed code)

```text
POST /auth/register {email,password}          → 201 {user id 4}
POST /auth/login {email,password,device_name} → 200 {access_token (EdDSA,
                                                kid ed25519-1), refresh_token, …}
GET  /auth/me            (no/garbage/tampered token) → 401
GET  /auth/me            (real token)                → 200 {user}
GET  /users, /auth/tokens (real token)              → 200
POST /auth/refresh {refresh_token}                   → 200 (new pair)
POST /auth/logout             (real token)           → 204
GET  /restaurants  + X-Request-ID echo               → 200, id propagated
```

## Appendix B — file map

```text
gateway  cmd/api/main.go                 config → middleware → routes → run
         internal/config/config.go       env-only config, required JWT_* fail fast
         internal/handlers/auth.go       RequireAuth (local verify, X-User-* set)
         internal/handlers/proxy.go      keep-path proxy, header allowlist
         internal/helpers/requestid.go   X-Request-ID context carry
         internal/jwt/jwt.go             Split/CheckHeader/VerifySignature/
                                         DecodeClaims/VerifyToken
identity app/Services/JwtService.php     issue + verify (EdDSA, iss/aud enforced)
         app/Services/AuthService.php    pairs, rotation, logout(-all), pwd change
         app/Http/Middleware/AuthenticateJwt.php  identity-side verify + user load
         app/Http/Controllers/AuthController.php  endpoint handlers
         config/jwt.php                  issuer/audience/kid/ttl/leeway
         routes/api.php                  /ready, /auth/*, /users, /roles, /permissions
         app/Console/Commands/GenerateJwtKeys.php key generation
```

## Appendix C — open items (not yet implemented)

1. `internal/jwt`: `LoadPublicKey` doesn't length-check (32 bytes) — the
   truncated-key incident booted fine and failed every token. Add the check.
2. ~~`.env.example`: missing `IDENTITY_SERVICE_URL` and all `JWT_*` — fresh
   checkouts can't boot. Add with placeholder values.~~ Done: `.env.example`
   now documents all required vars (key as placeholder to copy from identity).
3. Gateway never forwards `X-User-*` upstream and identity re-verifies the
   JWT itself, so the headers are currently informational. Either forward
   them (needs an identity trust rule) or document them as observability-only.
4. Key rotation is manual copy + container recreate; consider JWKS when a
   second verifier appears.
5. Refresh reuse-detection (kill token family on replay) — hardening, deferred.
