# syntax=docker/dockerfile:1

# --- 1. Build the admin console (static SPA) ---
FROM node:20-alpine AS web
RUN corepack enable && corepack prepare pnpm@9 --activate
WORKDIR /web
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm run build

# --- 2. Build the gateway (static, CGO-free; modernc sqlite is pure Go) ---
FROM golang:1.25 AS gobuild
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /light-gateway ./cmd/gateway

# --- 3. Minimal runtime: one binary that serves API + console ---
FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=gobuild /light-gateway /app/light-gateway
COPY --from=web /web/dist /app/web
ENV LIGHT_GATEWAY_WEB=/app/web \
    LIGHT_GATEWAY_DATA=/data/light-gateway.db \
    LIGHT_GATEWAY_ADDR=:7001
EXPOSE 7001
VOLUME ["/data"]
ENTRYPOINT ["/app/light-gateway"]
