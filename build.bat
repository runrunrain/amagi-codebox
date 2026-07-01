@echo off
chcp 65001 >nul 2>&1
title amagi-codebox 一键构建

echo ========================================
echo   amagi-codebox 一键构建
echo ========================================
echo.

cd /D "%~dp0"

echo [1/5] 检查依赖环境...
where wails >nul 2>&1
if errorlevel 1 (
    echo [提示] 未检测到 wails, 尝试自动安装...
    where go >nul 2>&1
    if errorlevel 1 (
        echo [错误] 未检测到 Go 环境, 请先安装 Go: https://golang.org/dl/
        pause
        exit /b 1
    )
    go install github.com/wailsapp/wails/v2/cmd/wails@latest
    if errorlevel 1 (
        echo [错误] wails 安装失败!
        pause
        exit /b 1
    )
    echo [提示] wails 安装完成.
    set "PATH=%PATH%;%USERPROFILE%\go\bin"
)
where wails >nul 2>&1
if errorlevel 1 (
    echo [错误] wails 仍无法找到, 请重新打开终端后再试.
    pause
    exit /b 1
)
echo [提示] wails 已就绪.
echo.

echo [2/5] 正在构建移动端前端...
echo.
where npm >nul 2>&1
if errorlevel 1 (
    echo [错误] 未检测到 npm, 移动端前端构建为必需步骤。请安装 Node.js: https://nodejs.org/
    pause
    exit /b 1
)
pushd mobile
call npm ci --prefer-offline
if errorlevel 1 (
    popd
    echo [错误] 移动端依赖安装失败!
    pause
    exit /b 1
)
call npm run build
if errorlevel 1 (
    popd
    echo [错误] 移动端前端构建失败!
    pause
    exit /b 1
)
popd
echo [提示] 移动端前端构建完成
echo.

echo [3/5] 正在构建项目...
echo.
set GIT_VERSION=
for /f "delims=" %%v in ('git describe --tags --abbrev^=0 2^>nul') do set GIT_VERSION=%%v
if not defined GIT_VERSION (
    rem 无 git tag 时回退到 wails.json productVersion，确保不显示 dev
    for /f "delims=" %%p in ('powershell -NoProfile -Command "(Get-Content wails.json ^| ConvertFrom-Json).info.productVersion" 2^>nul') do set GIT_VERSION=%%p
)
if not defined GIT_VERSION set GIT_VERSION=dev
set GIT_COMMIT=
for /f "delims=" %%c in ('git rev-parse --short HEAD 2^>nul') do set GIT_COMMIT=%%c
if not defined GIT_COMMIT set GIT_COMMIT=unknown
set BUILD_TIME=
for /f "delims=" %%t in ('powershell -NoProfile -Command "Get-Date -Format yyyy-MM-ddTHH:mm:ssZ" 2^>nul') do set BUILD_TIME=%%t
if not defined BUILD_TIME set BUILD_TIME=unknown
set GO_VER=
for /f "delims=" %%g in ('go env GOVERSION 2^>nul') do set GO_VER=%%g
if not defined GO_VER set GO_VER=unknown
echo [提示] 构建版本: %GIT_VERSION% (commit %GIT_COMMIT%, go: %GO_VER%)
wails build -ldflags "-X main.Version=%GIT_VERSION% -X main.GitCommit=%GIT_COMMIT% -X main.BuildTime=%BUILD_TIME% -X main.GoVersion=%GO_VER%"
if %ERRORLEVEL% neq 0 (
    echo.
    echo [错误] 构建失败!
    pause
    exit /b 1
)

echo.
echo [4/5] 正在复制到项目根目录...
copy /Y "build\bin\amagi-codebox.exe" "amagi-codebox.exe" >nul
if %ERRORLEVEL% neq 0 (
    echo [错误] 复制到根目录失败!
    pause
    exit /b 1
)

echo [5/5] 正在复制到用户目录...
if not exist "%USERPROFILE%\.amagi-codebox" mkdir "%USERPROFILE%\.amagi-codebox"
copy /Y "build\bin\amagi-codebox.exe" "%USERPROFILE%\.amagi-codebox\amagi-codebox.exe" >nul
if %ERRORLEVEL% neq 0 (
    echo [警告] 复制到用户目录失败(可能正在运行中)
)

echo.
echo ========================================
echo   构建完成!
echo   - 根目录: %~dp0amagi-codebox.exe
echo   - 用户目录: %USERPROFILE%\.amagi-codebox\amagi-codebox.exe
echo ========================================
echo.
pause
