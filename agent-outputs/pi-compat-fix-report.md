# Pi 兼容性补齐 — 修复报告

> 对标审核报告：`agent-outputs/pi-compat-review.md`
> 修复范围：P0-1、P1-1 ～ P1-7、P2-1、P2-4、P2-5（共 11 项，全部 P0/P1 + 本任务指定的 P2）
> 未在本任务范围：P2-2（前端 npm 示例占位文案）、P2-3（manifest glob 扫描）—— 需产品/前端单独确认，未擅自改动。

---

## 0. 验证证据（先给结论）

| 命令 | 结果 |
|---|---|
| `go build ./...` | ✅ PASS |
| `go vet ./...` | ✅ PASS |
| `go test ./...` | ✅ PASS（含 piplugin / appmeta/pi / usage / launcher / remote 全部包） |
| `npm --prefix frontend run build` | ✅ PASS（`vue-tsc --noEmit` + Vite；chunk-size 为既有 warning，无关） |
| `wails generate module` | ✅ 已重新生成 piplugin 绑定（`DO NOT EDIT` 头） |

新增/重写测试文件：
- `internal/appmeta/pi/parser_test.go`（重写：header 预读、lineage dedup、三类非-assistant usage、USD 币种、截断尾行）
- `internal/usage/pi_sync_test.go`（P2-1 双根 symlink 去重）
- `internal/launcher/pi_config_test.go`（P1-7 权限 + `$ENV:` 解析）
- `internal/piplugin/security_pinned_test.go`（P1-1 元字符 + P2-4 精确 semver）
- `internal/piplugin/executor_env_test.go`（P1-2 env 注入）

**未覆盖路径（诚实披露）**：
1. **真实 Wails 桌面交互冒烟**（L3）：本环境无可连接的桌面 Wails 运行时，无法做"打开 Pi tab → 列表 → 安装 → 重启验证加载"的端到端。P0-1 已通过"Bind 注册 + 绑定重生成 + 前端 import 全匹配 + TS 构建通过"的静态链路证明，但运行时冒烟需 `wukong`/`luoshen` 在桌面环境补做。
2. **真实 Pi session → usage sync 端到端**：parser/usage 行为由单测覆盖（含 fork fixture、增量两轮、截断恢复），但未跑真实 pi 写出的 JSONL。建议 `wukong` 用一次真实 pi 会话验证 DB 入库。
3. **Windows cmd.exe 注入**：P1-1 用元字符黑名单 + 单测防御；无法在 macOS 环境复现 `cmd.exe /c` 真实路径，建议 Windows CI 补回归。

---

## 1. 逐项修复映射

### P0-1：`PiPlugins` 未加入 Wails Bind ✅

**根因**：`app.PiPlugins` 已构造挂载，但 `main.go` 的 `Bind` 清单止于 `app.OpenCodePlugins`，运行时 `window.go.piplugin.Service` 不存在；前端用的是手写绑定（绕过生成链路）。

**修复**：
- `main.go`：Bind 清单增加 `app.PiPlugins`。
- 删除手写 `frontend/wailsjs/go/piplugin/Service.{js,d.ts}`。
- 执行 `wails generate module` 重新生成（自动产出含 `RefreshPackages / GetPackageDetails / InstallPackage / UpdatePackage / RemovePackage / ListInstalledPackages` 的正式绑定，并在 `models.ts` 注入 `piplugin` 命名空间）。
- 核对 `frontend/src/api/piPlugin.ts` 的 5 个 import 全部匹配新生成的导出，无需改动。

**验证**：`npm run build` 通过；`grep PiPlugins main.go` 命中 Bind。

---

### P1-1【安全】：Windows cmd.exe 命令注入面 ✅

**根因**：`validateSource` 只拒绝前导 `-` / NUL / CR/LF，未拒绝 `& | < > ^ % ( )` 等 cmd.exe 元字符。Windows 下 pi 是 `.cmd`，`ProcessRunner` 用 `cmd.exe /c pi.cmd <source>` 执行，source 中的元字符会被 cmd 解释。

**修复**（`internal/piplugin/service.go`）：新增 `cmdShellMetachars = "&|<>^%()"`，`validateSource` 命中任一字符即拒绝。三类合法源（npm/git/local）语法均不含这些字符，全量拒绝安全。

**防御性说明**：本项选黑名单而非"安全转义"，因为 (a) 三类合法源语法确定不含元字符，allowlist 等价且更简单；(b) Windows cmd 转义无标准库、易错，黑名单 + 语法校验是更可靠的边界防御。

**测试**：`TestValidateSourceRejectsCmdMetachars`（8 种注入向量）+ `TestValidateSourceAcceptsCleanSources`（7 种合法源回归）。

---

### P1-2：插件管理目录与 CodeBox Pi 运行目录脱节 ✅

**根因**：`piplugin.NewService("", log)` 在 CodeBox 进程未设 `PI_CODING_AGENT_DIR` 时管理 `~/.pi/agent`；而 `LaunchPiSession` 强制子进程 `PI_CODING_AGENT_DIR=~/.amagi-codebox/pi-runtime`。面板装的包不会出现在 CodeBox 启动的 Pi 会话里。

**修复**：
- `app.go`：`piplugin.NewService(filepath.Join(configDir, "pi-runtime"), log)` —— 管理目录与启动目录统一。
- `internal/piplugin/executor.go`：写操作（install/remove/update）fork pi CLI 时显式注入 `PI_CODING_AGENT_DIR=s.agentDir`（经 `launcher.BuildEnv`），确保 CLI 写到同一托管目录。
- `service.go` 包文档 + `NewService` 注释更新管理范围语义。

**测试**：`TestExecutePiCommandInjectsAgentDir` 断言注入的 env 值与 agentDir 一致、install 参数不变。

---

### P1-3：增量解析丢失 header 上下文 ✅

**根因**：header（session UUID、cwd）是 parser 调用内局部变量；从 `LastLineOffset` seek 后不再看到首行 header，新记录退化成"文件名 + 编码目录名"，同一 session 被拆成不同 ID。

**修复**（`internal/appmeta/pi/parser.go`）：新增 `readPiHeader(path)`，每次 `ExtractUsageRecords` 入口先预读首行拿 cwd/id/parentSession，再 seek 到 startOffset 解析 body。header 不可变，预读始终权威。

**测试**：`TestExtractUsageRecordsPiIncrementalSessionContext` —— 两轮同步后第二批记录的 `SessionID`/`ProjectDir` 与第一批一致（非文件名/编码目录）。

---

### P1-4：fork/clone 复制历史重复计费 ✅

**根因**：dedup key = `hash(filePath, entryID)`。pi 的 `createBranchedSession`/`forkFrom` 把原 entry 逐条复制到新文件（entry ID 不变），换文件后成新键，历史被二次计费。旧测试反而把这种重复行为固化成"预期"。

**修复**（`internal/appmeta/pi/parser.go`）：dedup key 改为 `hash(lineageRoot, entryID)`，其中 `lineageRoot` = 沿 `parentSession` 指针走到最顶层祖先的 canonical 路径。
- `resolveLineageRoot` + `walkLineageRoot`（带 `lineageMaxDepth=32` 防环、`EvalSymlinks` 规范化、`sync.Map` 缓存）。
- 复制的 entry 共享祖先 lineageRoot → 与原文件去重；同一文件内不同 branch 的 entry ID 不同 → 仍全计；不相关 session 即使 8-hex ID 撞车也不冲突。

**测试**：
- `TestExtractUsageRecordsPiForkNoDoubleCount`：fork 复制的 entry dedup key == 原文件 entry 的 key；fork 新增 entry 独立。
- `TestExtractUsageRecordsPiDedupCollisionSafe`：两个无 parentSession 关系的 session，相同 8-hex entry ID，dedup key 仍不同。

---

### P1-5：漏计 toolResult / compaction / branch_summary usage ✅

**根因**：parser 只接受 `type==message && role==assistant`，遗漏官方明确"计入 session token/cost 总量"的三类 entry。

**修复**（`internal/appmeta/pi/parser.go`）：`parsePiLine` 扩展为 4 类计费源：
- `message/assistant` —— 始终计（真实 turn，可能未计量）。
- `message/toolResult` 且 `message.usage` 非空 —— 计（工具内部 LLM 工作；无 provider/model，attribution 诚实留空）。
- `compaction` 且 `entry.usage` 非空 —— 计（压缩摘要生成）。
- `branch_summary` 且 `entry.usage` 非空 —— 计（分支摘要生成）。
- 新增 `usageHasData` 过滤纯零 usage，避免噪声。

**测试**：`TestExtractUsageRecordsPiNonAssistantUsage` —— 1 assistant + 1 toolResult(无 usage 跳过) + 1 toolResult(有 usage) + 1 compaction + 1 branch_summary = 4 计费记录，dedup key 全不同，USD 原生成本正确。

---

### P1-6：Pi 原生成本（美元）被误判为国产 provider 的 CNY ✅

**根因**：pi 的 `usage.cost.total` 以美元计价（见 pi-ai README），但 parser 留空 `CurrencyCode`，sync 调 `currencyForProvider` 把 GLM/Kimi/Qwen 等映射成 CNY，导致美元数值被当 CNY，后续折算差一个汇率倍。

**修复**：
- `parser.go`：`buildRecord` 在 `cost.total > 0` 时设 `CostProvided=true, CurrencyCode="USD", NativeCost=total×1e6`。
- `internal/usage/sync.go`：`syncPiJSONL` 透传 `st.CurrencyCode`（原先是硬留空）。仅当无原生成本（`CostProvided=false`，回退本地价格表）时才走 provider 币种推断。

**测试**：parser 样例 turn1（amagi-glm provider + 非零 cost）断言 `CurrencyCode=="USD"`、`CostProvided=true`、`NativeCost=17100`；turn2（零 cost）断言无 currency。

---

### P1-7【安全】：自定义 headers 凭据明文落盘 + 权限过松 ✅

**根因**：header 值（可能含 `Authorization`/`X-API-Key` secret）原样写入 0644 的 `pi-runtime/models.json`（目录 0755）；无 secret 引用机制，导出/备份配置会泄露凭据。

**修复**（`internal/launcher/pi_config.go`）：
- **权限收紧**：`WritePiAgentConfig` 改 `MkdirAll(0o700)` + `WriteFile(0o600)`。
- **`$ENV:` 引用约定**：header 值可写 `$ENV:VAR` 或 `${ENV:VAR}` 引用环境变量。配置文件只存引用字面量（可安全导出）；`BuildPiModelsConfig` 在构建时经 `resolveEnvHeaders` → `resolveEnvHeaderValue` 解析为 `os.Getenv(VAR)` 的实际值，只写入锁定的运行时 models.json。未设环境变量时该 header 被省略（不落空占位）。
- 正则 `envHeaderRefPattern` 要求整值即引用（不支持部分插值），避免明文与引用混在同一值。

**测试**：`TestWritePiAgentConfigTightPerms`（0700/0600）+ `TestResolveEnvHeaderValue`（6 种引用/字面量 case）+ `TestBuildPiModelsConfigResolvesEnvHeaders`（端到端：引用解析、字面量透传、未设引用被省略）。

---

### P2-1：双根枚举未做物理路径去重 ✅

**根因**：`enumeratePiSessionFiles` 直接 append 两个根的枚举结果，假设互不重叠；symlink/别名会让同一物理文件出现两次，dedup 又含未规范化的路径 → 重复入库。

**修复**（`internal/usage/sync.go`）：枚举后按 `EvalSymlinks` canonical path 去重（解析失败保留原路径）。

**测试**：`TestEnumeratePiSessionFilesDedupsSymlink`（一个真实文件 + 另一根 symlink 指向同目录 → 只枚举一次）+ `TestEnumeratePiSessionFilesBothRootsDistinct`（两个真实独立文件都返回）。

---

### P2-4：npm 任意版本表达式都标 pinned ✅

**根因**：`inspectPackage` 用 `ref != ""` 判 npm pinned，导致 `^1.2.0`/`~1.2.0`/`latest`/`*` 都显示 pinned，与 pi 官方（仅 exact semver 视为 pinned）不符。

**修复**：
- `internal/piplugin/source.go`：新增 `isExactSemver` + `exactSemverPattern`（`^v?\d+\.\d+\.\d+(-...)?(+...)?$`）。
- `internal/piplugin/service.go`：npm 路径改用 `isExactSemver(parsed.ref)`；git 任意非空 ref 仍算 pinned。

**测试**：`TestIsExactSemver`（6 exact / 10 非 exact）+ `TestNpmPinnedOnlyForExactSemver`（端到端 6 种 source → pinned 标志）。

---

### P2-5：增量 parser 假设 EOF 总有换行，缺尾行保护 ✅

**根因**：scanner 把 `len+1` 计入 offset，并发写/异常中断留下的半条 JSON（无换行）被跳过后 offset 已持久化，后续补全的内容可能被越过。

**修复**（`internal/appmeta/pi/parser.go`）：改用 `bufio.Reader.ReadString('\n')`，**仅当行以 `\n` 结尾才推进 committed offset**；不完整尾行（无 `\n`，EOF）不提交，保留旧 offset 供下次同步重读。

**测试**：`TestExtractUsageRecordsPiTruncatedTail` —— 写 header+turn1+半条 turn2 → 只得 1 记录、offset 停在 turn1 换行处（< 文件大小）；补全 turn2 后从该 offset 续读 → 正确得到 turn2。

---

## 2. Changed Files

| 文件 | 变更 |
|---|---|
| `main.go` | Bind 增加 `app.PiPlugins`（P0-1） |
| `app.go` | `NewService(filepath.Join(configDir,"pi-runtime"), …)`（P1-2） |
| `internal/piplugin/service.go` | `cmdShellMetachars` + `validateSource` 元字符拒绝（P1-1）；npm pinned 改 `isExactSemver`（P2-4）；包文档/NewService 注释（P1-2） |
| `internal/piplugin/source.go` | `isExactSemver` + `exactSemverPattern`（P2-4） |
| `internal/piplugin/executor.go` | 写操作注入 `PI_CODING_AGENT_DIR`（P1-2） |
| `internal/appmeta/pi/parser.go` | **重写**：header 预读（P1-3）、lineage-root dedup（P1-4）、4 类计费源（P1-5）、USD 币种（P1-6）、`ReadString`+committed offset（P2-5） |
| `internal/usage/sync.go` | 透传 `CurrencyCode`（P1-6）；`enumeratePiSessionFiles` EvalSymlinks 去重（P2-1） |
| `internal/launcher/pi_config.go` | 0700/0600 权限（P1-7）；`resolveEnvHeaders`/`$ENV:` 解析（P1-7） |
| `frontend/wailsjs/go/piplugin/Service.{js,d.ts}` | 删除手写 → `wails generate module` 重生成（P0-1） |
| `frontend/wailsjs/go/models.ts` | 生成器自动追加 `piplugin` 命名空间（P0-1） |

新增测试（见第 0 节）。

## 3. 回滚说明

全部为源码增量修改，无数据迁移/无 schema 变更/无依赖增删。回滚 = `git checkout` 上述文件 + 删除新增测试文件 + 删除 `frontend/wailsjs/go/piplugin/`（重生成产物）。`lineageRootCache`（sync.Map）为进程内缓存，无落盘状态。

## 4. 建议下一步

1. **L3 运行时冒烟**（`wukong`/`luoshen`，桌面环境）：打开 Extensions→Pi → 列表/空态 → 详情 → 安装 `npm:` source → 更新 → 移除 → 重启 CodeBox Pi 验证 package 实际加载。这是 P0-1 的真正放行证据。
2. **真实 pi session → usage sync 端到端**（`wukong`）：跑一次真实 pi 会话，核对 DB 入库的 session/project 一致性、fork 不重复、非-assistant usage 计入、USD 币种。
3. **Windows cmd 注入回归**（`wukong`，Windows CI）：P1-1 黑名单在 Windows 真实 `cmd.exe /c` 路径下的回归。
4. **P2-2 / P2-3**（产品/前端确认后回流）：npm 示例占位文案、manifest glob 扫描——不在本次范围。
5. 本任务多处涉及 pi session schema 假设（lineage、4 类 usage、币种），已用真实 session-manager.js + session-format.md 逐字段核对并加 fixture；但 **真实 pi 版本升级后 schema 漂移** 仍是长期风险，建议 `wukong` 维护一组真实抓取的 JSONL 黄金样本。
