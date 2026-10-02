# ---------- 阶段一：构建前端 ----------
FROM node:22-alpine AS web
WORKDIR /app/web
COPY web/package.json web/package-lock.json* ./
RUN npm ci --no-audit --no-fund || npm install --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ---------- 阶段二：构建后端 ----------
FROM golang:1.22-alpine AS backend
WORKDIR /src
ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOPROXY=https://goproxy.cn,direct
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /app/web/dist ./web/dist
ARG VERSION=0.1.0
RUN go build -trimpath \
      -ldflags "-s -w -X portbridge/internal/version.Version=${VERSION} -X portbridge/internal/version.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
      -o /out/portbridge ./cmd/portbridge

# ---------- 阶段三：运行镜像 ----------
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -u 10001 portbridge
WORKDIR /app
COPY --from=backend /out/portbridge /app/portbridge
COPY --from=web /app/web/dist /app/web/dist
RUN mkdir -p /app/data /app/config && chown -R portbridge:portbridge /app
USER portbridge

EXPOSE 13255 13256
VOLUME ["/app/data"]

ENTRYPOINT ["/app/portbridge"]
CMD ["-c", "/app/config/config.yaml"]
