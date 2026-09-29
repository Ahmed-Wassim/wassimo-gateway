FROM golang:1.27.1-bookworm AS builder

WORKDIR /src

COPY go.mod go.sum ./

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /api ./cmd/api

FROM gcr.io/distroless/static

COPY --from=builder /api /api

USER nonroot

EXPOSE 8080

CMD ["/api"]
