# =========================================================================
# OMNIPULSE API-GATEWAY — Production Multi-Stage Build
# =========================================================================
# Build context: repository root (omnipulse/)
# =========================================================================

# --- Stage 1: Builder ---
FROM golang:1.25-alpine AS builder

WORKDIR /workspace

# Copy workspace-level dependency files first (layer caching)
COPY go.work go.work.sum ./
COPY shared/go.mod ./shared/
COPY apps/api-gateway/go.mod apps/api-gateway/go.sum ./apps/api-gateway/

# Download dependencies
RUN cd apps/api-gateway && go mod download

# Copy source code
COPY shared/ ./shared/
COPY apps/api-gateway/ ./apps/api-gateway/

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/api-gateway ./apps/api-gateway/cmd/main.go

# --- Stage 2: Minimal Runtime ---
FROM gcr.io/distroless/static-debian12

COPY --from=builder /bin/api-gateway /api-gateway

EXPOSE 8080

ENTRYPOINT ["/api-gateway"]
