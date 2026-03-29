@echo off
chcp 65001 >nul 2>&1
title amagi-codebox 一键构建

echo ========================================
echo   amagi-codebox 一键构建
echo ========================================
echo.

cd /D "%~dp0"

echo [0/3] 检查依赖环境...
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

echo [1/3] 正在构建项目...
echo.
wails build
if %ERRORLEVEL% neq 0 (
    echo.
    echo [错误] 构建失败!
    pause
    exit /b 1
)

echo.
echo [2/3] 正在复制到项目根目录...
copy /Y "build\bin\amagi-codebox.exe" "amagi-codebox.exe" >nul
if %ERRORLEVEL% neq 0 (
    echo [错误] 复制到根目录失败!
    pause
    exit /b 1
)

echo [3/3] 正在复制到用户目录...
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
