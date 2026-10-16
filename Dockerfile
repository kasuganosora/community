# ============================================
# Stage 1: Build Go binary
# ============================================
FROM golang:1.27.1-alpine3.24@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS go-builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /src

# Cache dependencies
# go.mod has a local replace (gomatrixserverlib => ./third_party/gomatrixserverlib),
# so the replaced module must be present before go mod download.
COPY go.mod go.sum ./
COPY third_party/ ./third_party/
RUN go mod download

# Copy source
COPY . .

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /build/community ./cmd/community
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /build/community-transfer ./cmd/community-transfer
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /build/tootctl ./cmd/tootctl

# ============================================
# Stage 2: Build frontend
# ============================================
FROM node:26.10.0-alpine3.24@sha256:0b36e8c136b94cd4fcf02188228e76c31ad5872eef3fec8cbd2eee500cfd9e80 AS frontend-builder

WORKDIR /src/frontend
RUN npm install --global npm@12.2.0
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci

COPY frontend/ ./
RUN npm run build

# ============================================
# Stage 3: Runtime
# ============================================
FROM alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6

# python3 + psql run the Misskey exporter used by the setup wizard.
RUN apk add --no-cache ca-certificates tzdata ffmpeg python3 postgresql17-client

WORKDIR /app

# Copy binary
COPY --from=go-builder /build/community .
COPY --from=go-builder /build/community-transfer .
# Admin CLI, e.g. `docker compose exec community tootctl accounts create ...`
COPY --from=go-builder /build/tootctl /usr/local/bin/tootctl

# Copy frontend dist
COPY --from=frontend-builder /src/frontend/dist ./frontend/dist

# Copy resources (i18n, mail templates, oauth HTML)
COPY resources ./resources

# Misskey exporter used by the setup wizard's migration mode.
COPY scripts/migration/export_misskey.py ./scripts/migration/export_misskey.py
COPY scripts/migration/export_mastodon_db.py ./scripts/migration/export_mastodon_db.py

# Create data directories (uploads are stored relative to the working dir)
RUN mkdir -p uploads config data

# Env: the app reads /app/config/.env (written by the /setup wizard; mount
# /app/config as a volume). Process environment variables override it.
# APP_ROOT anchors frontend/dist / resources.
ENV GIN_MODE=release \
    APP_ROOT=/app \
    ENV_FILE=/app/config/.env

EXPOSE 8080

ENTRYPOINT ["./community"]
