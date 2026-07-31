# =========================================================================
# OMNIPULSE COMPLIANCE-ENGINE — Production Multi-Stage Build
# =========================================================================
# Build context: repository root (omnipulse/)
# =========================================================================

# --- Stage 1: Builder ---
FROM golang:1.25-alpine AS builder

WORKDIR /workspace

# Copy workspace-level dependency files first (layer caching)
COPY go.work go.work.sum ./
COPY shared/go.mod ./shared/
COPY apps/api-gateway/go.mod apps/api-gateway/go.sum* ./apps/api-gateway/
COPY apps/broadcast-worker/go.mod apps/broadcast-worker/go.sum* ./apps/broadcast-worker/
COPY apps/compliance-engine/go.mod apps/compliance-engine/go.sum* ./apps/compliance-engine/

# Download dependencies
RUN cd apps/compliance-engine && go mod download

# Copy source code
COPY shared/ ./shared/
COPY apps/api-gateway/ ./apps/api-gateway/
COPY apps/broadcast-worker/ ./apps/broadcast-worker/
COPY apps/compliance-engine/ ./apps/compliance-engine/

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/compliance-engine ./apps/compliance-engine/cmd/main.go

# --- Stage 2: Minimal Runtime ---
FROM gcr.io/distroless/static-debian12

COPY --from=builder /bin/compliance-engine /compliance-engine

ENTRYPOINT ["/compliance-engine"]
