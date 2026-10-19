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
COPY apps/api-gateway/go.mod apps/api-gateway/go.sum* ./apps/api-gateway/
COPY apps/broadcast-worker/go.mod apps/broadcast-worker/go.sum* ./apps/broadcast-worker/
COPY apps/compliance-engine/go.mod apps/compliance-engine/go.sum* ./apps/compliance-engine/

# Download dependencies
RUN cd apps/api-gateway && go mod download

# Copy source code
COPY shared/ ./shared/
COPY apps/api-gateway/ ./apps/api-gateway/
COPY apps/broadcast-worker/ ./apps/broadcast-worker/
COPY apps/compliance-engine/ ./apps/compliance-engine/

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/api-gateway ./apps/api-gateway/cmd/main.go

# --- Stage 2: Runtime with local document OCR tools ---
FROM debian:bookworm-slim

# Tesseract handles OCR and Poppler renders scanned PDF pages to images.
# Keep the runtime image minimal and remove package metadata afterwards.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        poppler-utils \
        tesseract-ocr \
        tesseract-ocr-eng \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /bin/api-gateway /api-gateway

EXPOSE 8080

ENTRYPOINT ["/api-gateway"]
