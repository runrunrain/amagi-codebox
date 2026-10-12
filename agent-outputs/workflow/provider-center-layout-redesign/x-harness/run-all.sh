#!/bin/bash
# 批量生成 screenshots/（命名 {view}-{state}.png）。分批执行，每批 < 3 分钟：
#   ./run-all.sh base      9 视图 × 2 视口（1280×800 / 800×600，full 数据）
#   ./run-all.sh expanded  展开全部卡片（高视口，看完整内容）
#   ./run-all.sh states    loading / error / empty 三类状态 × 视图
#   ./run-all.sh special   catalog-error / bad-json / long / skin
# 前置：vite dev 在 127.0.0.1:5199（npm --prefix frontend run dev -- --port 5199 --strictPort --host 127.0.0.1）
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT="$HERE/../screenshots"
mkdir -p "$OUT"
S="$HERE/shoot.sh"
VIEWS="presets-anthropic presets-openai pi-agents pi-registry pi-auth omp-agents omp-registry opencode-presets opencode-global"
EDITORS="pi-agents pi-registry pi-auth omp-agents omp-registry opencode-presets opencode-global"

case "$1" in
  base)
    for dim in "1280 800" "800 600"; do set -- $dim
      for v in $VIEWS; do "$S" full "$v" "$1" "$2" "$OUT/$v-$1x$2.png"; done
    done ;;
  expanded)
    for spec in "pi-agents 1280 1500" "pi-registry 1280 1500" "pi-auth 1280 1000" "omp-agents 1280 900" "opencode-global 1280 1500" \
                "pi-agents 800 1900" "pi-registry 800 1900" "omp-agents 800 1100" "opencode-global 800 1700"; do
      set -- $spec; "$S" full "$1" "$2" "$3" "$OUT/$1-expanded-$2x$3.png" "__ab.expandAll()"
    done ;;
  states)
    for v in $VIEWS; do "$S" loading "$v" 1280 800 "$OUT/$v-loading.png"; done
    for v in $EDITORS; do "$S" error "$v" 1280 800 "$OUT/$v-error.png"; done
    for v in $VIEWS; do "$S" empty "$v" 1280 800 "$OUT/$v-empty.png" "__ab.expandAll()"; done ;;
  special)
    "$S" full,catalog-error pi-agents 1280 800 "$OUT/pi-agents-catalog-error.png"
    "$S" full,catalog-error omp-agents 1280 800 "$OUT/omp-agents-catalog-error.png"
    for v in pi-agents pi-registry pi-auth omp-agents omp-registry opencode-global; do "$S" bad-json "$v" 1280 800 "$OUT/$v-bad-json.png"; done
    "$S" full,long presets-anthropic 1280 800 "$OUT/presets-anthropic-long.png"
    "$S" full,long presets-anthropic 800 600 "$OUT/presets-anthropic-long-800x600.png"
    "$S" full,long pi-agents 800 600 "$OUT/pi-agents-long-800x600.png"
    "$S" full,long opencode-presets 800 600 "$OUT/opencode-presets-long-800x600.png"
    "$S" full,long omp-registry 800 600 "$OUT/omp-registry-long-800x600.png" ;;
  *) echo "usage: $0 base|expanded|states|special"; exit 2 ;;
esac
echo "batch $1 done"
