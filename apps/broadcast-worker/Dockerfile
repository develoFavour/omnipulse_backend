# =========================================================================
# OMNIPULSE BROADCAST-WORKER — Production Multi-Stage Build
# =========================================================================
# Build context: repository root (omnipulse/)
# =========================================================================

# --- Stage 1: Builder ---
FROM golang:1.25-alpine AS builder

WORKDIR /workspace

# Copy workspace-level dependency files first (layer caching)
COPY go.work go.work.sum ./
COPY shared/go.mod ./shared/
COPY apps/broadcast-worker/go.mod ./apps/broadcast-worker/

# Download dependencies
RUN cd apps/broadcast-worker && go mod download

# Copy source code
COPY shared/ ./shared/
COPY apps/broadcast-worker/ ./apps/broadcast-worker/

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/broadcast-worker ./apps/broadcast-worker/cmd/main.go

# --- Stage 2: Minimal Runtime ---
FROM gcr.io/distroless/static-debian12

COPY --from=builder /bin/broadcast-worker /broadcast-worker

ENTRYPOINT ["/broadcast-worker"]
