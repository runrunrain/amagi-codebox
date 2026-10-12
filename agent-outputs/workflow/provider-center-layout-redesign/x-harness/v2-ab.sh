#!/bin/bash
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export AGENT_BROWSER_SESSION="p2r-v2-e826c0fb2831"
export AGENT_BROWSER_INIT_SCRIPTS="$HERE/v2-mock-wails.js,$HERE/v2-helpers.js"
export AGENT_BROWSER_ARGS="--disable-background-timer-throttling,--disable-renderer-backgrounding,--disable-backgrounding-occluded-windows"
exec agent-browser "$@"
