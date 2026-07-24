# ============================================
# Stage 1: Build Go binary
# ============================================
FROM golang:1.24-alpine AS go-builder

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
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata ffmpeg

WORKDIR /app

# Copy binary
COPY --from=go-builder /build/community .

# Copy frontend dist
COPY --from=frontend-builder /src/frontend/dist ./frontend/dist

# Copy resources (i18n, mail templates, oauth HTML)
COPY resources ./resources

# Create data directories
RUN mkdir -p storage/uploads

# Env
ENV GIN_MODE=release

EXPOSE 8080

ENTRYPOINT ["./community"]