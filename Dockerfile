# SPA: hasil vite build ditulis ke /src/web/dist
FROM node:20-alpine AS web
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY frontend/ ./
RUN npm run build

# Go: server dan worker, statis tanpa cgo
FROM golang:1.26-alpine AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web/embed.go ./web/
COPY web/templates ./web/templates
COPY web/static ./web/static
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

# Image akhir: alpine supaya ada sertifikat CA, zona waktu, dan wget untuk healthcheck.
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -H -u 10001 app
WORKDIR /app
COPY --from=go /out/server /out/worker ./
COPY --from=web /src/web/dist ./web/dist
USER app
EXPOSE 8080
CMD ["/app/server"]
