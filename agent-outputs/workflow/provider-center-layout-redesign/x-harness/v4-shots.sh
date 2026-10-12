#!/bin/bash
# v4 九视图基准截图；每张输出进度与 DOM 度量。两档独立运行，每批 ≤3min。
set -e
set -o pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT="$HERE/../screenshots"
AB="bash $HERE/v4-ab.sh"
PORT="${V4_PORT:-5199}"
W="${1:-1280}"; H="${2:-800}"
for view in presets-anthropic presets-openai pi-agents pi-registry pi-auth omp-agents omp-registry opencode-presets opencode-global; do
  $AB set viewport "$W" "$H" > /dev/null
  $AB open "http://127.0.0.1:$PORT/?s=full&run=$RANDOM#/provider" > /dev/null
  $AB eval "__ab.seq('$view')" > /dev/null
  $AB wait --fn "!!document.querySelector('.pc-detail-content .editor-toolbar, .pc-detail-content .pc-toolbar')" > /dev/null
  $AB screenshot "$OUT/v4-$view-${W}x${H}.png" > /dev/null
  echo "shot $view $W x $H"
  $AB eval '__ab.metrics()'
done
