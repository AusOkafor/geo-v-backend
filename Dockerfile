FROM golang:1.23-alpine AS builder
WORKDIR /app

# Install tzdata so we can copy zoneinfo into the scratch image
RUN apk add --no-cache tzdata ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build all binaries — strip debug info to minimize image size
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/api     ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/worker  ./cmd/worker
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/migrate ./cmd/migrate

# Copy migrations so the pre-deploy command can find them
RUN mkdir -p /migrations && cp -r migrations/* /migrations/

# ─── Runtime ─────────────────────────────────────────────────────────────────
FROM scratch

# TLS certificates for HTTPS calls to AI APIs and Shopify
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

# Timezone data for scheduler (time.Local in periodic jobs)
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Binaries
COPY --from=builder /bin/api     /bin/api
COPY --from=builder /bin/worker  /bin/worker
COPY --from=builder /bin/migrate /bin/migrate

# Migrations (used by pre-deploy command on Render)
COPY --from=builder /migrations /migrations

# Default to API — worker overrides with dockerCommand in render.yaml
CMD ["/bin/api"]
