# Pi 远程启动 + 用量同步 实现报告

> Task B（remote launch endpoint + usage statistics）· work 型
> 产出 Agent：luban · 落盘时间：2025-01

---

## 一、变更摘要

为 amagi-codebox 补齐 Pi 的远程启动端点与会话用量统计，完全对标已有的 codex / opencode 模式：

1. **远程启动（Sub-item 1）**：新增 `POST /api/sessions/launch-pi` 路由与 `handleLaunchPi`（镜像 `handleLaunchCodex`，调用 `App.LaunchPiSession`）；`/api/sessions/launch-meta` 的响应增加 `pi` 段（providers 全集 + terminal preset 选项，复用 `buildLaunchPresetOptions(configSvc, "pi")` 与 `DefaultPiPresets()`）。
2. **用量统计（Sub-item 2）**：新增 `appPi` 常量、dedup / 计费分支、Pi 会话 JSONL 同步链路；独立实现 `internal/appmeta/pi` 解析器（格式与 claude/codex 不同）。

未改动 `app.go` 的 `LaunchPiSession` 逻辑、`internal/piplugin`（另一并行任务在建）、`internal/launcher`（pi_config.go 的 headers/compat 透传为另一任务产物，非本次改动）。

---

## 二、改动文件清单

### 新增文件（本次）

| 文件 | 作用 |
|------|------|
| `internal/appmeta/pi/parser.go` | Pi 会话 JSONL 用量解析器（`ExtractUsageRecords`），独立实现，`encoding/json` 逐行解码 |
| `internal/appmeta/pi/parser_test.go` | 解析器单测：典型样本 / 增量 resume / 坏行跳过 / file-scoped dedup 四组用例 |

### 修改文件（本次）

| 文件 | 改动 |
|------|------|
| `internal/usage/types.go` | 新增 `appPi = "pi"`、`dedupPrefixPi = "pi:"` 常量 |
| `internal/usage/cost.go` | `ComputeBillableInput` 新增 `case appPi`：返回 `inputTokens` 不扣减（pi 的 usage.input 已是 fresh input，不含 cacheRead） |
| `internal/usage/service.go` | `generateDedupKey` 新增 `case appPi`：`"pi:" + hash16(model, sessionID, occurredAt)`（兜底；正常路径 dedup key 由 parser 预填） |
| `internal/usage/metadata.go` | **仅注释**：在 codex unknown-provider 归一化块后补注释说明 Pi 无需等价分支（Pi 是多 provider，猜测单一默认会误标；codebox `amagi-` 命名空间已在 sync 阶段剥离，存档即 canonical；任何残余缺口由通用 `inferProviderFromModel` 覆盖）。**不写死代码分支**——Pi sync 全新、无历史脏数据，metadata.go 的 pi 专用分支属于不可达死代码（4.7 防御） |
| `internal/usage/sync.go` | ① import pi 包；② `SyncAll` 在 opencode 步骤后插入「第 4 步 Pi jsonl」并发同步，rollup 顺延为第 5 步；③ 新增 `normalizePiProvider`（剥离 `amagi-` 前缀）、`enumeratePiSessionFiles`（双根枚举）、`syncPiJSONL`（增量同步，镜像 claude/codex）、`updateSyncStatePi` |
| `internal/remote/app_interface.go` | `AppInterface` 新增 `LaunchPiSession(...)` 方法签名（`*App` 已有该方法） |
| `internal/remote/handlers.go` | ① 注册 `POST /api/sessions/launch-pi`；② 新增 `handleLaunchPi`（body 字段 modelName/providerID/mode/workDir/shellPath，与 codex 一致）；③ `handleGetLaunchMeta` 填充 `Pi` 段 |
| `internal/remote/session_launch_types.go` | `launchMetadataResponse` 新增 `Pi launchMetaSection` 字段 |
| `internal/remote/websocket_test.go` | **3 行**：为测试 mock `websocketTestApp` 补 `LaunchPiSession` 桩方法（`return "", errors.New("not implemented")`）。原因——给 `AppInterface` 加方法后，所有实现者必须满足接口；生产侧 `*App` 已实现，唯一另一个实现者是该测试 mock，不补会编译失败 |

### 非本次改动（git status 可见，属并行 piplugin 任务，已核验未触碰）

- `app.go`（`PiPlugins` 字段挂载）
- `internal/config/types.go`（`PiCompat` / `Headers` / `AuthHeader` 字段）
- `internal/launcher/pi_config.go`（headers/compat 透传）
- `internal/piplugin/`（新建，另一任务）

---

## 三、Pi JSONL 同步的数据源路径逻辑

Pi 的会话 JSONL 落盘位置由 `PI_CODING_AGENT_DIR` 决定（Pi 默认 `~/.pi/agent`，下含 `sessions/`）。codebox 启动 Pi 时注入隔离的 `PI_CODING_AGENT_DIR=<configDir>/pi-runtime`（configDir 即 `~/.amagi-codebox`），因此两类会话写在不同根目录，布局完全一致：

```
<root>/sessions/--<cwd 斜杠转短横>--/<unix-ms>_<uuid>.jsonl
```

`enumeratePiSessionFiles(configDir, home)` 枚举两处：

1. **`<configDir>/pi-runtime/sessions`（隔离目录，必扫、主源）**——codebox 启动的所有 Pi 会话都在此。
2. **`~/.pi/agent/sessions`（Pi 默认目录，可选覆盖）**——用户自行启动 Pi 的会话。

两根不重叠（`configDir=~/.amagi-codebox` ≠ `~/.pi`），同一文件不会出现两次，无需跨根去重；同一解析器、同一 dedup 规则处理。文件内 assistant 消息的 dedup 由 `dedup_key` 跨次扫描幂等保证（见下）。

**与运行中会话的写入冲突**：sync 增量读取已落盘行（`last_line_offset` 续传），Pi 增量追加写新行；正在写的尾部不完整行在下次 sync 时才被读到。dedup key 基于 `(file, entry-id)` 稳定，重复扫描幂等。

---

## 四、dedup / 计费语义决策

### dedup key

- **格式**：`"pi:" + sha1(absFilePath, entryID)[:16]`。
- **为何带文件路径**：Pi 的 entry id 仅 8-hex，会话内基本唯一但跨会话/跨文件不保证唯一（不同文件可能出现相同短 id）。把 `absFilePath` 纳入 hash，确保 file-scoped 唯一，杜绝跨文件误合并。
- **预填位置**：parser 在解析时直接预填 `stub.DedupKey`（含会话文件绝对路径）；`usage.service.generateDedupKey` 的 `case appPi` 仅为兜底（不包含文件路径的弱版本）。

### 计费 / cost

- **`CostProvided=true` 当且仅当 `usage.cost.total > 0`**：Pi 对每条 assistant 消息从 provider/model 定价算出权威聚合成本。非零时信 Pi 的总额（`NativeCost = total × 1e6`，micro-native-currency），与 OpenCode 消费 `session.cost` 一致。
- **`total == 0` 时 `CostProvided=false`**：未计费 provider（如免计量）——回退到本地价格表估算，由 `usage.service` 处理，parser 不猜。
- **`CurrencyCode` 留空**：由 `eventToRecord` 在 CostProvided 路径按 provider 经 `currencyForProvider` 推断，不在 parser / sync 重复维护币种表。
- **`amagi-` 命名空间剥离**：codebox 以 `amagi-<provider>` 命名 Pi provider 做隔离；在 **sync 阶段**（`normalizePiProvider`）剥离前缀，使 Pi 用量并入 canonical provider 桶（计价、币种、看板分组一致）。**不在 parser 剥离**（parser 保持原始记录透传，职责单一）；**不在 metadata.go backfill 剥离**（Pi sync 全新无历史脏数据，该处分支为死代码，4.7）。
- **`ComputeBillableInput(appPi)` 返回 `inputTokens` 不扣减**：Pi 的 `usage.input` 已是 fresh input（不含 cacheRead，与 claudecode/opencode 同语义），cacheRead 仅作为独立维度计费，不参与 input 扣减。
- **`CostProvided` 机制的已取舍**：Pi 实际提供 4 维 cost 明细（input/output/cacheRead/cacheWrite），但当前 `UsageEvent`/`eventToRecord` 的 CostProvided 路径只消费总额（为 OpenCode 设计）。丢弃明细是有意为之——上报明细需扩展 `UsageEvent` 结构，超出本任务范围（记入「建议下一步」，不镀金）。

### 时间戳解析

优先 `message.timestamp`（Unix ms，最精确）→ 回退 entry 级 ISO `timestamp`（RFC3339Nano）→ 回退文件 mtime。

---

## 五、验证结果（实际执行命令与结果）

| 命令 | 结果 |
|------|------|
| `go build ./...` | **exit 0**（全量构建通过） |
| `go vet ./...` | **exit 0**（唯一输出是 `internal/secrets` 的 macOS Keychain cgo 弃用 warning，与本次改动无关、改动前已存在） |
| `go test -count=1 ./internal/appmeta/...` | **ok** `internal/appmeta/pi`（含新解析器 4 组用例）· claude/codex/opencode 全绿 |
| `go test -count=1 ./internal/usage/...` | **ok**（appPi / ComputeBillableInput / generateDedupKey 改动未破坏既有测试） |
| `go test -count=1 ./internal/remote/...` | **ok**（含修好的 `websocketTestApp` mock） |
| `go test -count=1 ./internal/config/...` | **ok**（顺带回归，无影响） |

新增解析器单测覆盖：① 典型会话样本（header + user + 2 assistant 含 cost + toolResult/model_change 噪声）；② byte-offset 增量 resume（仅读新增行）；③ 坏 JSON 行跳过不中断；④ 同 entry id 跨文件产生不同 dedup key（file-scoped）。

**验证强度**：L1 冒烟（build + vet）+ L2 单测（parser 4 组 + 既有 usage/remote 回归）。未做 L3 端到端（需真实 codebox 运行时启动 Pi 产生 JSONL，属主上环境验证，见下）。

---

## 六、未覆盖路径 / 限度披露

1. **端到端（L3）未做**：未在真实 codebox 进程内启动 Pi、产生会话 JSONL、再触发 sync 全链路验证。解析器逻辑已用贴近 `session-format.md` 的 fixture 单测覆盖，但「codebox 注入 PI_CODING_AGENT_DIR 后 JSONL 真实落到 `<configDir>/pi-runtime/sessions`」这一路径依赖运行时，建议主上启动一次 Pi 会话后触发用量刷新确认（4.7 schema 假设端到端核验）。
2. **launch-pi 端点未发 HTTP 实测**：handler 逻辑镜像已验证的 codex/opencode，且经 `go vet` + remote 包测试回归；真实 HTTP 调用属运行时验证。
3. **cost 明细丢弃**：见第四节末（有意取舍，记入建议下一步）。

---

## 七、建议下一步

1. **L3 端到端验证（建议升级 medium 由 diting 审核前补做）**：主上在 codebox 启动一次 Pi 会话 → 确认 `~/.amagi-codebox/pi-runtime/sessions/` 下生成 JSONL → 触发用量 sync → 看板出现 pi 记录、cost / provider（已剥 `amagi-`）/ model 字段正确。
2. **前端对接**：本端点对标 codex/opencode，前端可复用同一启动组件传 `modelName`/`providerID`；launch-meta 的 `pi` 段已就绪。
3. **（可选，非本任务）cost 4 维明细上报**：若需 cacheRead/cacheWrite 分项计价，需扩展 `UsageEvent` 携带 Pi 的 cost 明细并改 `eventToRecord`——属独立增强，本次不镀金。
4. **提交**：建议 taibai 提交时将「Pi remote + usage（本次）」与「piplugin 并行任务」分两次 commit（已确认两组改动文件不交叉）。

---

## 八、回滚说明

本次改动均为**新增/追加**，无对既有逻辑的语义破坏：
- 新常量 `appPi` / `dedupPrefixPi`、新 `case` 分支（default 行为不变）、新文件、新路由——回滚即删除对应新增 + 还原 `AppInterface`/`launchMetadataResponse` 字段 + 还原 `websocket_test.go` 3 行。
- `metadata.go` 仅注释，回滚无功能影响。
- sync.go 的「第 4 步 Pi」为独立步骤，删除不影响 claude/codex/opencode/rollup。
