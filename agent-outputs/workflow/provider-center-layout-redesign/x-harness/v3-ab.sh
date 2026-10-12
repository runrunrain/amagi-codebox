#!/bin/bash
# v3：agent-browser 包装。transport 与布局无关，直接复用 v2-mock-wails.js；导航/测量助手换成 v3-helpers.js。
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export AGENT_BROWSER_SESSION="p2v3-48794d8b7dfa"
export AGENT_BROWSER_INIT_SCRIPTS="$HERE/v2-mock-wails.js,$HERE/v3-helpers.js"
export AGENT_BROWSER_ARGS="--disable-background-timer-throttling,--disable-renderer-backgrounding,--disable-backgrounding-occluded-windows"
exec agent-browser "$@"
