# Wassimo API Gateway

Part of the [Wassimo](https://github.com/ahmed-wassim) food-delivery platform.
Single public entry point — routes client requests to the backend services.

```text
Repo: github.com/ahmed-wassim/wassimo-gateway
```

## What it does

Routing, request IDs, timeouts, CORS, and error mapping. It owns no business
logic and no database: prices, statuses, and filtering belong to the services
behind it.

## Stack

```text
Go + standard-library net/http ServeMux
```

## Run

Requires the Catalog service reachable (see `github.com/ahmed-wassim/wassimo-catalog`).

```powershell
go run ./cmd/api                # gateway on localhost:9051
```

Or in Docker on the shared network with the other Wassimo services:

```powershell
docker network create wassimo-network   # once
docker compose up -d --build            # gateway on localhost:9051
```

## Configuration

```text
PORT                 gateway port (default 9051 locally, 8080 in container)
ENV                  dev | prod (.env files load only when ENV != prod)
CATALOG_SERVICE_URL  catalog base URL (localhost:9050 locally, http://app:8080 in Docker)
```

Copy `.env.example` to `.env` and adjust locally. `.env` is never committed
and never baked into images.

## Behavior

```text
GET /restaurants... (any catalog path + query, any method) → forwarded verbatim, status/body copied
X-Request-ID: generated when missing, returned on every response, propagated upstream
Upstream failure or timeout (5s) → 502 {"error":"upstream unavailable"}
```

## Layout

```text
cmd/api/            entrypoint: config → middleware → routes → run
internal/config/    environment-only configuration
internal/handlers/  proxies: forward + copy, no validation, no parsing
internal/helpers/   request-ID context carry
```
