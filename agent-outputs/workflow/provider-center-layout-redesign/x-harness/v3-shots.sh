#!/bin/bash
# v3：九视图截图（agent-browser），输出 screenshots/v3-<view>-WxH.png，并打印每个视图的 __ab.metrics()。
set -e
set -o pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT="$HERE/../screenshots"
AB="bash $HERE/v3-ab.sh"
PORT="${V3_PORT:-5199}"
# Each batch is bounded to nine views, with progress for every screenshot.
W="${1:-1280}"; H="${2:-800}"
for view in presets-anthropic presets-openai pi-agents pi-registry pi-auth omp-agents omp-registry opencode-presets opencode-global; do
  $AB set viewport "$W" "$H" > /dev/null
  $AB open "http://127.0.0.1:$PORT/?s=full&run=$RANDOM#/provider" > /dev/null
  $AB eval "__ab.seq('$view')" > /dev/null
  $AB wait --fn "!!document.querySelector('.pc-detail-content .editor-toolbar, .pc-detail-content .pc-toolbar')" > /dev/null
  $AB screenshot "$OUT/v3-$view-${W}x${H}.png" > /dev/null
  echo "shot $view $W x $H"
  $AB eval '__ab.metrics()'
done
