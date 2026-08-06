#!/usr/bin/env bash
# run-perf.sh — M4-B 性能套件的「独立显式入口」（M4-INT R1 / diting M4-001）
# ---------------------------------------------------------------------------
# 背景：M4-B 性能测量（e2e/perf/m4-b-performance.spec.ts）自带环境契约
#   M4B_ROUND=<正整数>，且经独立 runner e2e/perf/playwright.perf.config.ts 运行。
# 默认 Playwright 配置（playwright.config.ts）已全局 testIgnore 排除 e2e/perf/**，
#   所以 `npx playwright test` 不会跑它——必须经本脚本 / 直接指定 --config 显式运行。
#
# 用法：
#   ./e2e/perf/run-perf.sh 1        # 跑第 1 轮（独立 Playwright 进程）
#   ./e2e/perf/run-perf.sh 2        # 跑第 2 轮（独立 Playwright 进程）
#   ./e2e/perf/run-perf.sh 1 2      # 依次跑第 1、2 轮（两轮独立采样）
#   M4B_ARTIFACT_DIR=/tmp/perf ./e2e/perf/run-perf.sh 1   # 自定义产物目录
#
# 每轮是一个独立 Playwright 进程（workers=1、fullyParallel=false、retries=0、
#   timeout=15min），产出 raw/round-N.json 等证据，供 aggregate-results.mjs 做 R-7 统计。
# 两轮独立性的契约见 laojun M4-B 实现报告与本套件 spec 头部说明。
# ---------------------------------------------------------------------------
set -euo pipefail

CONFIG="e2e/perf/playwright.perf.config.ts"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <round...>   例: $0 1   或   $0 1 2" >&2
  exit 64
fi

for round in "$@"; do
  if ! [[ "$round" =~ ^[1-9][0-9]*$ ]]; then
    echo "run-perf.sh: 轮次必须是正整数，收到: '$round'" >&2
    exit 64
  fi
  echo "==> M4-B perf round ${round} (独立 runner: ${CONFIG}, M4B_ROUND=${round})"
  M4B_ROUND="$round" npx playwright test --config="$CONFIG"
done
