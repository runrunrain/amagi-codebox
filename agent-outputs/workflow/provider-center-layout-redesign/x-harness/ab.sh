#!/bin/bash
# agent-browser 包装：固定 session + 一致的启动选项（init script 走环境变量，launch hash 不变就不会重启浏览器）。
# 用法：x-harness/ab.sh <agent-browser 子命令...>   前置：vite dev 在 127.0.0.1:5199 运行
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-p2x-e826c0fb2831}"
export AGENT_BROWSER_INIT_SCRIPTS="$HERE/mock-wails.js,$HERE/helpers.js"
# 无头会话 document.visibilityState 为 hidden 时，Chrome 会把 ≥250ms 的定时器节流到 1000ms，
# 使 InfoTip 的 hover 延迟（250/120ms）与滚动事件时序测试失真——关闭后台节流相关特性。
export AGENT_BROWSER_ARGS="--disable-background-timer-throttling,--disable-renderer-backgrounding,--disable-backgrounding-occluded-windows,--disable-features=IntensiveWakeUpThrottling"
exec agent-browser "$@"
