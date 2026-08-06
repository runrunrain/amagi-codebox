# 鲁班自测报告：codex 解析 token too long + opencode github 插件兜底

- 任务：修复两个已实测定根因的 Bug
- 日期：2026-08-06
- tier：medium（两个独立 Bug，含解析核心路径与外部 CLI 交互，建议 diting 审核）

## 一、实现摘要

### Bug 1：codex 会话解析 "token too long"（主 bug）

根因：`bufio.Scanner` 固定 16MiB 缓冲上限，主上 4 个 codex rollout 单行达 17.8~30.4MB（base64/大 tool_result），超上限导致整文件解析报 `bufio.Scanner: token too long`，usage 永久缺失且每 5 分钟重复扫描百 MB 级文件。

修复：把逐行读取从 `bufio.Scanner` 改为 `bufio.Reader.ReadBytes('\n')`（按需扩容，无上限），参照同仓库 `internal/appmeta/pi/parser.go` 既有做法。

- `internal/appmeta/codex/parser.go`
  - `ExtractUsageRecordsWithContext`：Scanner → `bufio.NewReaderSize(f, 64*1024)` + `ReadBytes('\n')`。控制流由原 `continue` 改为 `switch` 分支（保证 `readErr` 检查总在 `if len(rawLine) > 0` 块之后执行，避免 EOF 后死循环）。
  - `ReadUsageContext`：同样改造，保留 `offset >= endOffset` 提前 break 语义。
- `internal/appmeta/claude/jsonl.go`
  - `ExtractUsageRecords`：同类隐患一并改造（claude 实测当前最长行 1.27MB 未爆，预防性修复）。
  - `ExtractFirstUserMessage`（1MB cap，非 usage 链路）与 `internal/proxy` scanner **未动**（Contract 明确）。

关键语义保留：
- offset 记账：`currentOffset += int64(len(rawLine))`（ReadBytes 返回值含 `\n`，长度即实际字节数），修正了旧实现末尾无 `\n` 时 `+1` 多算一字节的隐患，断点续传 offset 更精确。
- 末行无 `\n`：`ReadBytes` 返回 `io.EOF` 且 data 非空时仍处理该行并计入 offset → lastOffset 精确等于文件字节大小。
- 空行跳过、单行 JSON 解析失败不中断、session_meta/turn_context/event_msg 处理逻辑、返回值语义全部不变。

### Bug 2：OpenCode 插件 github spec 安装/更新失效

根因：opencode CLI 1.18.10 对所有 `github:` spec 执行 `opencode plugin <spec> --global [--force]` 必报 `NpmInstallFailedError`，但 Arborist 实际已把包装入 `~/.cache/opencode/packages/<spec>/`，失败发生在入口点解析阶段，且**失败时不写 opencode.json 配置**。

修复：CLI 优先 + 失败兜底，兼容未来修好的 CLI。

- `internal/opencodeplugin/service.go`
  - 新增 `ensurePluginSpecInConfig(oldSpec, newSpec)`：复用 `UninstallPlugin` 的 JSON/JSONC 校验 + gjson/sjson + `writeAtomic` 口径；把 newSpec 写入 plugin 数组（不重复添加），oldSpec 非空且存在则移除；配置不存在时按 `{}` 起步创建；JSONC 含注释无法安全编辑时按 UninstallPlugin 口径报错。
  - `InstallPlugin`：CLI 返回 error 后，若 `inspectPlugin(spec,false).InstallPath != ""`（cache 已装好）则自行写入配置返回成功（Output 注明走了本地兜底）；cache 未就绪原样返回 CLI 错误。
  - `UpdatePlugin`：CLI 失败后，先 `inspectPlugin(target.Spec,false)` 确认 cache 已装好且 `Version == target.Version`，再 `ensurePluginSpecInConfig(spec, target.Spec)` 切换，最后 `verifyUpdatedPlugin` 收口；任一环节不达标返回原始 CLI 错误。
  - 兜底仅在 CLI 返回 error 时触发，CLI 成功路径完全不变；`s.mu` 锁已覆盖，config 手术在锁内。

## 二、Artifact 路径

- 本报告：`agent-outputs/luban/20260806-codex-token-toolong-opencode-github-fallback/self-test-report.md`
- 改动源码（6 文件）：
  - `internal/appmeta/codex/parser.go`（修改）
  - `internal/appmeta/claude/jsonl.go`（修改）
  - `internal/appmeta/codex/parser_test.go`（新建）
  - `internal/appmeta/claude/jsonl_test.go`（新建）
  - `internal/opencodeplugin/service.go`（修改）
  - `internal/opencodeplugin/service_test.go`（修改）

## 三、引用上游 artifact

- Contract（本任务书）：两个已实测定根因的 Bug 修复要求。
- 参考实现：`internal/appmeta/pi/parser.go` L193-218（`bufio.Reader.ReadString` 无上限方案）。
- 根因实证文件：`~/.codex/sessions/2026/07/22/rollout-...019f88c3....jsonl`（单行 30,392,471 字节）。

## 四、验证/自检结果

### L1 冒烟
- `go build -mod=vendor ./internal/appmeta/... ./internal/opencodeplugin/...` 通过。
- `go vet -mod=vendor ./...` 全量通过（无输出）。

### L2 单元测试（go test -count=1，非缓存）
- `./internal/appmeta/codex/...` ok 2.996s（共 6 测试）
  - 既有（未改逻辑，回归通过）：`TestExtractUsageRecordsCodexSample`（2 条 token_count、provider/model/dedup/offset）、`TestExtractUsageRecordsCodexResume`（断点续传 + ReadUsageContext）。
  - 新增：`TestExtractUsageRecordsHandlesOversizedLine`（20MB 单行不报 token too long，超长行后 token_count 正确提取 in=100/out=60/cr=20，offset==文件大小）、`TestExtractUsageRecordsOffsetMatchesFileSizeNoTrailingNewline`（末尾无 \n lastOffset==文件大小）、`TestExtractUsageRecordsResumesFromOffset`（断点续传无重复）、`TestReadUsageContextHandlesOversizedLine`（ReadUsageContext 吞 20MB 行，后续 turn_context 正常）。
- `./internal/appmeta/claude/...` ok 1.054s（jsonl_test.go + jsonl_usage_test.go）
  - 既有（jsonl_test.go，未动 ExtractFirstUserMessage 路径，回归通过）：`TestSessionJSONLPath`、`TestExtractFirstUserMessage`（11 子测试）、`TestExtractFirstUserMessage_FileNotExist`、`TestFindLatestActiveJSONL`（5 子测试）。
  - 既有（jsonl_usage_test.go，验证 ExtractUsageRecords 改动保留语义，回归通过）：`TestExtractUsageRecordsClaudeSample`、`TestExtractUsageRecordsClaudeBadJSONContinues`（坏 JSON 不中断）。
  - 新增（jsonl_test.go 末尾追加）：`TestExtractUsageRecordsHandlesOversizedLine`（20MB user 行跳过，assistant usage 提取，offset==文件大小）、`TestExtractUsageRecordsOffsetMatchesFileSizeNoTrailingNewline`（末尾无 \n offset 精确）。
- `./internal/opencodeplugin/...` ok 2.122s（含 4 个新兜底测试 + 既有测试全绿）
  - `TestUpdateGitHubPluginFallsBackWhenCLIFailsButCacheReady`：git 用 fake runner 拦截返回预置 tag（无真实网络），opencode 假失败但写 cache → 兜底成功，config 切到 target.Spec，旧 spec 移除。
  - `TestUpdateGitHubPluginReturnsCLIErrWhenCacheMissing`：CLI 失败且 cache 缺失 → 返回原始错误，config 不变。
  - `TestInstallPluginFallsBackWhenCLIFailsButCacheReady`：Install 假失败但 cache 就绪 → 兜底成功，spec 入配置。
  - `TestInstallPluginReturnsCLIErrWhenCacheMissing`：Install 失败且 cache 缺失 → 返回原始错误，config 为空。
- `./internal/usage/...` ok 2.576s（未改动，回归通过）

### L3 真实端到端（可选加分，已做）
- 临时 `manual_e2e` 测试对真实 30.4MB 单行 rollout（213,730,086 字节）跑 `ExtractUsageRecordsWithContext`：
  - `记录数=810 文件大小=213730086 lastOffset=213730086 provider=openai model=gpt-5.6-sol`，2.38s，无 token too long。
  - 验证后已删除临时 e2e 测试文件，仓库无残留。

### 自检清单（第七节）
1. 行动兑现：两 Bug 均按 Contract 实现，语义保留。PASS
2. 构建与验证跑过且通过，L1 冒烟未省，L3 真实文件已端到端。PASS
3. 无骨架残留：无 TODO/空实现/假数据/固定返回/日志式错误处理。PASS
4. 一次一功能：改动均在 Contract 范围内，无夹带。**过程披露（4.2 创建前检查一度违反，已纠正）**：初建 codex/parser_test.go、claude/jsonl_test.go 时，glob 用了默认工作目录（`/`）而非项目目录 amagi-codebox，误判"No files found"，用 write 覆盖了两个**已存在**的测试文件。自查 git status 发现显示 ` M`（tracked）而非 `??` 后立即核查，确认 HEAD 版已有测试，已恢复原版并合并新增测试（codex：原 2 测试 + 新 4 测试；claude：原版 jsonl_test.go 全量保留 + 既有 jsonl_usage_test.go 未受影响 + 新增 2 测试），最终全量回归通过。
5. Bug 回归证据：Bug1 有根因实证 + 修复后 20MB 单测 + 30MB 真实文件 e2e；Bug2 有 cache 就绪/缺失两路测试。PASS
6. 不涉及 hook/toolInput/schema。N/A
7. 报告含 changed files + 回滚说明 + 未覆盖路径披露。PASS
8. 【待反思】建议见第五节。PASS

## 五、建议下一步

1. **审核**：本任务触及解析核心路径（usage 计费准确性）与外部 CLI 交互兜底（config 写入），属中风险，建议 diting 审核位重点看：
   - codex/claude offset 语义变化（旧实现末尾无 \n 会多算 1 字节，新实现精确）是否影响既有 sync_state 断点续传——理论上只会修正历史偏差，不会回退或重复计费，但值得确认调用方（usage 同步）对 offset 单调性的假设。
   - opencodeplugin 兜底写入 config 的并发与原子性（writeAtomic 已保证，但与 CLI 并发场景可复核）。
2. **提交**：交 taibai；commit 建议拆两个（Bug1 parser / Bug2 opencodeplugin），便于回溯。
3. **【待反思】候选**：「jsonl 逐行读取设固定缓冲上限」是反复踩坑模式——codex(16MiB 仍不够，单行 30MB)、claude(16MiB)、而 pi 已用无上限 ReadString。建议沉淀一条战术手册条目：凡解析外部 agent 写入的 jsonl（codex/claude/opencode/pi 等），逐行读取一律用 `bufio.Reader.ReadBytes/ReadString`（按需扩容），禁止 `bufio.Scanner` 设经验上限——因为单行长度由 base64 图片/大 tool_result 决定，无理论上界，任何经验值都会被更大的真实行击穿。
4. **遗留风险/未覆盖路径披露**：
   - claude ExtractUsageRecords 改动为预防性修复，无真实 16MB+ claude 文件可端到端验证（主上 claude 当前最长行 1.27MB），仅 20MB 构造单测覆盖。
   - opencodeplugin 兜底用 fake runner 模拟，未在真实 opencode 1.18.10 CLI + 真实 github 插件上端到端验证（需真实环境）；根因与 cache 布局由 Contract 实证给定，兜底逻辑覆盖 cache 已装好/未装好两路。
   - 兜底依赖 `inspectPlugin` 能从 cache 定位包：若未来 opencode 改变 cache 目录命名（`openCodeCacheKey`）或包结构，兜底会静默退化为返回 CLI 错误（不会误写配置），属安全降级。

## 回滚说明
所有改动为纯代码替换/新增，无数据迁移、无 schema 变更、无依赖新增。回滚 = git revert 对应 commit（taibai 职责）；代码层面 parser 改动可逆（恢复 Scanner）、兜底改动可逆（恢复原 Install/Update 直接 return executeOpenCodeCommand），均无破坏性副作用。
