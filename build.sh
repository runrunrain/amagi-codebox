#!/usr/bin/env bash
# amagi-codebox 跨平台一键构建脚本（macOS / Linux）。
# 与 build.bat 对齐：通过 wails build -ldflags 注入版本信息。
# 版本来源：git tag 优先，无 tag 时回退 wails.json info.productVersion，最终 dev。
set -euo pipefail

cd "$(dirname "$0")"

echo "========================================"
echo "  amagi-codebox 一键构建 (Unix)"
echo "========================================"
echo

if ! command -v wails >/dev/null 2>&1; then
    echo "[错误] 未检测到 wails，请先安装: go install github.com/wailsapp/wails/v2/cmd/wails@latest"
    exit 1
fi
if ! command -v go >/dev/null 2>&1; then
    echo "[错误] 未检测到 go，请先安装 Go: https://golang.org/dl/"
    exit 1
fi

if [ -d frontend ]; then
    echo "[1/3] 安装前端依赖并构建..."
    (cd frontend && npm ci && npm run build)
fi

if [ -d mobile ]; then
    echo "[2/3] 构建移动端前端..."
    if [ -f package.json ] && grep -q '"build:mobile"' package.json; then
        npm run build:mobile
    fi
fi

echo "[3/3] 构建项目..."
GIT_VERSION=""
if command -v git >/dev/null 2>&1; then
    GIT_VERSION="$(git describe --tags --abbrev=0 2>/dev/null || true)"
fi
if [ -z "$GIT_VERSION" ]; then
    # 无 git tag 时回退到 wails.json productVersion，确保不显示 dev
    if command -v python3 >/dev/null 2>&1 && [ -f wails.json ]; then
        GIT_VERSION="$(python3 -c "import json,sys;print(json.load(open('wails.json')).get('info',{}).get('productVersion',''))" 2>/dev/null || true)"
    fi
fi
if [ -z "$GIT_VERSION" ]; then
    GIT_VERSION="dev"
fi

GIT_COMMIT="unknown"
if command -v git >/dev/null 2>&1; then
    GIT_COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
fi
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo unknown)"
GO_VER="$(go version 2>/dev/null || echo unknown)"

echo "[提示] 构建版本: ${GIT_VERSION} (commit ${GIT_COMMIT}, go: ${GO_VER})"

wails build -ldflags "-X main.Version=${GIT_VERSION} -X main.GitCommit=${GIT_COMMIT} -X main.BuildTime=${BUILD_TIME} -X main.GoVersion=${GO_VER}"

if [ $? -eq 0 ]; then
    echo
    echo "[完成] 构建成功，产物位于 build/bin/"
else
    echo
    echo "[错误] 构建失败"
    exit 1
fi
