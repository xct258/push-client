@echo off
REM Windows 本机构建 push-client.exe（需安装 Go）
go vet ./...
if errorlevel 1 exit /b 1
go test ./...
if errorlevel 1 exit /b 1
go build -o push-client.exe ./cmd/push-client
dir push-client.exe
