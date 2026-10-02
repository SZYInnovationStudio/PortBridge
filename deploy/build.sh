#!/usr/bin/env bash
# PortBridge 交叉编译脚本：构建前端静态资源 + 多架构 Linux 后端二进制
# 用法：./deploy/build.sh [版本号]
set -euo pipefail

VERSION="${1:-0.1.0}"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${ROOT_DIR}/bin"

cd "${ROOT_DIR}"

echo "==> 构建前端 (web/dist)"
pushd web >/dev/null
if [ -f package-lock.json ]; then npm ci --no-audit --no-fund; else npm install --no-audit --no-fund; fi
npm run build
popd >/dev/null

echo "==> 构建后端 (linux/amd64, linux/arm64)"
mkdir -p "${OUT_DIR}"
LDFLAGS="-s -w -X portbridge/internal/version.Version=${VERSION} -X portbridge/internal/version.BuildTime=${BUILD_TIME}"

for arch in amd64 arm64; do
  echo "    - linux/${arch}"
  CGO_ENABLED=0 GOOS=linux GOARCH="${arch}" \
    go build -trimpath -ldflags "${LDFLAGS}" \
    -o "${OUT_DIR}/portbridge-linux-${arch}" ./cmd/portbridge
done

echo "==> 完成，产物位于 ${OUT_DIR}"
ls -lh "${OUT_DIR}"
