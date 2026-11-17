---
name: code-sweep
description: 对 amagi-codebox（Go + Wails 多前端仓库）做代码优化清理与 bug 扫描修复：跑 golangci-lint、govulncheck、knip 三条扫描线，按 P0–P3 严重度归并成清单，逐项最小化修复并跑回归测试。当用户提到"代码清理 / 清理一下项目 / bug 扫描 / 扫一下 bug / 代码优化 / 消技术债 / lint 存量清零 / code sweep"等意图时使用，即使用户没有点名具体工具。
---

# code-sweep：代码清扫（扫描 → 归并 → 修复 → 回归）

## 0. 这个仓库的事实（所有命令以此为准）

- Go 1.26 单 module（`amagi-codebox`），根目录 `package main` + `app_*.go`，子包在 `internal/`、`cmd/`；`vendor/` 已提交，不属于维护面——任何扫描结果与修改动作都跳过它。
- 三个 npm 子项目：`frontend/`（Vite + Vitest + ESLint）、`webui-claude/`、`mobile/`（Vitest）。根 `package.json` 只做编排，没有自己的源码。
- `.golangci.yml` 已存在且 pinned v2.12.2，只启用 govet / staticcheck(仅 SA*、S1*) / errcheck / ineffassign / unused。**ST\*/QF\*（风格与重构建议）是被有意排除的——不要去"修复"它们**。历史存量盘点见 `reports/s6-go-lint.md`。
- 根目录的 `envcheck.test`、`platform.test`、`secrets.test`、`updater.test` 是本地遗留的测试二进制（已被 gitignore），不是源码——不要读、不要改，扫描输出里出现直接忽略。
- 运行环境是 Windows + Git Bash。测试文件位置与命名规范见 `CLAUDE.md` 和 `docs/developer/testing.md`。

## 1. 前置检查

1. `git status` 必须干净（或只剩用户明确知晓的改动）。不干净就先停下让用户提交或 stash——清扫的改动必须能独立成 commit、可整体回滚。
2. 工具就绪检查。缺哪个就读 `references/tool-setup.md` 装一下；**两分钟内装不上就走降级路线，不要让清扫卡在装工具上**：

| 扫描线 | 工具 | 检查命令 | 缺失时降级 |
|---|---|---|---|
| Go 缺陷 | golangci-lint | `golangci-lint version` | `go vet ./...`（只覆盖 govet 一层，汇报时注明） |
| Go 漏洞 | govulncheck | `command -v govulncheck` | 跳过该线，汇报时注明 |
| 前端死代码 | knip | `npx knip --version` | 各子项目内 `npx tsc --noEmit` + `npx eslint .` |

## 2. 扫描（三条线独立跑，原始输出先落盘再分析）

`tmp/` 已被 gitignore，扫描中间产物都放这里：

```bash
mkdir -p tmp

# Go 缺陷（自动读仓库 .golangci.yml；10m 超时是配置定的，别用 --timeout 覆盖）
golangci-lint run ./... > tmp/sweep-golangci.txt 2>&1

# Go 依赖漏洞
govulncheck ./... > tmp/sweep-vuln.txt 2>&1

# 前端死代码 / 未用依赖（每个子项目各跑一次，knip 无需预装）
for d in frontend webui-claude mobile; do
  (cd "$d" && npx knip > "../tmp/sweep-knip-$d.txt" 2>&1)
done
```

分析 knip 输出时忽略：`wailsjs/`（Wails 生成的绑定，误报重灾区）、`dist/`、`node_modules/`。

## 3. 归并分级

读 `references/triage.md` 的完整规则（含误报甄别清单）。核心动作：同一根因合并成一项，按最坏严重度定级，输出一张表给用户过目：

| # | 级别 | 来源 | 位置 | 问题 | 根因分组 |
|---|---|---|---|---|---|
| 1 | P0 | govulncheck | 依赖 golang.org/x/crypto | GO-2026-xxxx 高危 | 漏洞-x/crypto |
| 2 | P1 | errcheck | app_wsl_supply.go:123 | 未检查 error | wsl-supply 错误吞噬 |

- **P0** 安全漏洞（govulncheck 中/高危）、明确正确性 bug（staticcheck SA\* 类）
- **P1** 缺陷风险——未检查的 error、无效赋值、可疑的类型断言
- **P2** 死代码与清理——unused、knip 的未用导出/文件/依赖
- **P3** 风格与低收益——**默认不做**（仓库已决策排除 ST\*/QF\*，别翻案）

## 4. 修复

把分级表给用户，确认修复范围（默认建议 P0+P1 全修、P2 逐项过目、P3 不动）。确认后：

- 一项一个最小改动，行为保持优先于"顺手重构"；每项修完立刻跑第 5 步的快速回归，绿了再进下一项。
- P2 删代码前确认它不是：Wails 绑定入口、测试夹具、被 `//go:build` 门控的分支、公共 API（哪怕看似无人调用）。
- 修 errcheck 命中时先看 `.golangci.yml` 的 exclude 列表：defer Close 等清理惯例不要硬加 `if err != nil`；其余未检错误要么显式处理，要么 `//nolint:errcheck // 理由`。
- 修正确性 bug（P0/P1 里的 SA\* 类）必须先写一个能复现的失败测试再修；回归测试命名遵循 CLAUDE.md——同目录 `<topic>_test.go`，不要引入新的批次号前缀。

## 5. 回归验证（每项小跑，收尾全量跑）

```bash
go build ./...                     # 每项修完必跑
go test ./... -count=1             # 收尾全量（与 CI 同款）
go test -race ./internal/session   # 动过并发包时加跑（按实际涉及的包替换）
cd frontend && npm run build       # 动了 frontend 时
cd frontend && npx vitest run      # frontend 有单测时（mobile 同理在其目录跑）
```

## 6. 汇报格式

收尾时给用户一张结果表 + 三段话：

- **已修复**：清单 #编号 → 一句话改动 + 验证方式
- **跳过**：清单 #编号 → 原因（误报 / 需产品决策 / 修复成本大于收益）
- **剩余存量**：P2/P3 没动的数量与去向建议（如记入 `reports/`）

## 红线

- 不动 `vendor/`、不删测试、不做纯风格重写；govulncheck 命中的依赖升级单独列出征询，不夹带在其他修复里。
- 每处修复独立可回滚；用户要求提交时遵循仓库 conventional commits（`fix(lint): ...`、`chore(cleanup): ...`），用户没要求就不要主动 commit。
- 扫描原始输出留在 `tmp/` 供复查，不提交。
