#!/bin/bash
# 构建 Linux 二进制 + Windows exe
set -e
cd "$(dirname "$0")"
go vet ./...
go test ./...
go build -o push-client ./cmd/push-client
GOOS=windows GOARCH=amd64 go build -o push-client.exe ./cmd/push-client
ls -la push-client push-client.exe
