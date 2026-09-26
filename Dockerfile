# ============================================
# Stage 1: Build Go binary
# ============================================
FROM golang:1.26-alpine AS go-builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /src

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /build/community ./cmd/community

# ============================================
# Stage 2: Build frontend
# ============================================
FROM node:26-alpine AS frontend-builder

WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci

COPY frontend/ ./
RUN npm run build

# ============================================
# Stage 3: Runtime
# ============================================
FROM alpine:3.24

RUN apk add --no-cache ca-certificates tzdata ffmpeg

WORKDIR /app

# Copy binary
COPY --from=go-builder /build/community .

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