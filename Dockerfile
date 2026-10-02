# ---------- 阶段一：构建前端 ----------
FROM node:22-alpine AS web
WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
# 使用 npm ci 保证依赖与 lockfile 完全一致（可复现构建）
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ---------- 阶段二：构建后端 ----------
FROM golang:1.22-alpine AS backend
WORKDIR /src
# GOPROXY 可通过 --build-arg GOPROXY=... 覆盖（默认使用国内镜像加速）
ARG GOPROXY=https://goproxy.cn,direct
ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOPROXY=${GOPROXY}
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

EXPOSE 23255 23256
VOLUME ["/app/data"]

ENTRYPOINT ["/app/portbridge"]
CMD ["-c", "/app/config/config.yaml"]
