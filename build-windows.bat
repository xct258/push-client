@echo off
REM Windows 本机构建 GUI 版（需安装 Go；CGO 需要 gcc，不装则 GUI 无法编译）
go vet ./...
if errorlevel 1 exit /b 1
go test ./...
if errorlevel 1 exit /b 1
go build -ldflags "-H windowsgui" -o push-client.exe ./cmd/push-client
dir push-client.exe
