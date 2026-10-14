# ============================================
# Stage 1: Build Go binary
# ============================================
FROM golang:1.27.2-alpine3.24@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS go-builder

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

RUN apk add --no-cache ca-certificates tzdata ffmpeg

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

# Create data directories (uploads are stored relative to the working dir)
RUN mkdir -p uploads

# Env: configuration comes from environment variables (see docker-compose.yml
# env_file/environment); APP_ROOT anchors .env / frontend/dist / resources.
ENV GIN_MODE=release \
    APP_ROOT=/app

EXPOSE 8080

ENTRYPOINT ["./community"]
