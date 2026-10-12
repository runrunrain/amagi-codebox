#!/bin/bash
# 单张截图：shoot.sh <scenario> <view> <W> <H> <out.png> [post-js]
#   scenario  mock 场景（见 mock-wails.js），如 full / empty / error / loading / catalog-error / bad-json / long / skin
#   view      presets-anthropic | presets-openai | pi-agents | pi-registry | pi-auth | omp-agents | omp-registry | opencode-presets | opencode-global
#   post-js   可选：截图前在页面里执行的 JS（如 __ab.expandAll()）
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
AB="$HERE/ab.sh"
SC="$1"; VIEW="$2"; W="$3"; H="$4"; OUT="$5"; POST="$6"
$AB set viewport "$W" "$H" >/dev/null
$AB open "http://127.0.0.1:5199/?s=${SC}&n=$$-$RANDOM#/provider" >/dev/null
sleep 1.2
SEQ=$($AB eval "__ab.seq('${VIEW}')")
case "$SEQ" in *"NOT FOUND"*) echo "WARN seq: $SEQ" ;; esac
sleep 1.6
if [ -n "$POST" ]; then $AB eval "$POST" >/dev/null; sleep 0.6; fi
$AB screenshot "$OUT" >/dev/null && echo "shot: $OUT"
