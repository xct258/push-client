#!/bin/bash
# 构建 Linux 版 + Windows GUI 版（exe 无黑窗）
set -e
cd "$(dirname "$0")"
go vet ./...
go test ./...
go build -o push-client ./cmd/push-client
if command -v x86_64-w64-mingw32-gcc >/dev/null; then
  CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc GOOS=windows GOARCH=amd64 \
    go build -ldflags "-H windowsgui" -o push-client.exe ./cmd/push-client
else
  echo "缺少 mingw（apt install gcc-mingw-w64），跳过 Windows 构建"
fi
ls -la push-client push-client.exe 2>/dev/null
