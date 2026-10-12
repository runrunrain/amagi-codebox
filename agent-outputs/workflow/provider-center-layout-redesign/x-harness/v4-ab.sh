#!/bin/bash
# v4 独立 agent-browser 会话，沿用隔离 transport，替换导航/度量助手。
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export AGENT_BROWSER_SESSION="p2v4-8079acb-luoshen"
export AGENT_BROWSER_INIT_SCRIPTS="$HERE/v2-mock-wails.js,$HERE/v4-helpers.js"
export AGENT_BROWSER_ARGS="--disable-background-timer-throttling,--disable-renderer-backgrounding,--disable-backgrounding-occluded-windows"
exec agent-browser "$@"
