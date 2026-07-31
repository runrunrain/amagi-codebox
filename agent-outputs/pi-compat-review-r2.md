# Pi coding agent 兼容性增量复审（R2）

## 结论

**不通过**。

本轮 11 项宣称修复中：7 项已修复，2 项部分修复，1 项未修复，1 项静态链路已修复但仍缺桌面 L3 证据。必跑的 Go build 与指定测试全绿；但 P1-7 的 headers 持久化链路实际会把新增字段整体清掉，且 P1-4 在祖先 session 文件不可用时仍会让 fork 复制历史重复计费。另有 2 个 Minor。

## 1. 增量基线

- 首轮已审基线：`HEAD 797f4907e43746950d6da2fe482995c0645e998b` 的全量工作区 diff，报告 `agent-outputs/pi-compat-review.md`，结论“不通过（1 P0 + 7 P1 + 5 P2）”。
- 本轮基线：仅复核 `agent-outputs/pi-compat-fix-report.md` 声明的修复增量及其必要上下文，不重审首轮其余 diff。
- 限制：修复前后没有独立 Git commit/index checkpoint，因此“fix diff”无法由 Git 单独重建；本轮按 fix-report 的 changed-files 映射与当前文件内容核验。

### 1.1 本轮 fix changed files 与范围摘要

| 文件 | 本轮变更范围 |
|---|---|
| `main.go` | Wails Bind 增加 `app.PiPlugins` |
| `app.go` | Pi package service 固定到 `<configDir>/pi-runtime` |
| `internal/piplugin/service.go` | Windows 元字符拒绝；npm pinned 判定调用 |
| `internal/piplugin/source.go` | `isExactSemver` |
| `internal/piplugin/executor.go` | 写操作注入 `PI_CODING_AGENT_DIR` |
| `internal/piplugin/security_pinned_test.go` | Windows 拒绝与 pinned 测试 |
| `internal/piplugin/executor_env_test.go` | agent dir 环境注入测试 |
| `internal/appmeta/pi/parser.go` | header 预读、lineage dedup、非 assistant usage、USD、完整尾行提交 |
| `internal/appmeta/pi/parser_test.go` | 增量 header、fork、非 assistant usage、截断尾行 fixtures |
| `internal/usage/sync.go` | 双根 canonical 去重、Pi currency 透传 |
| `internal/usage/pi_sync_test.go` | 双根 symlink fixture |
| `internal/launcher/pi_config.go` | `$ENV:` 解析与 runtime 权限 |
| `internal/launcher/pi_config_test.go` | env 解析、runtime 权限测试 |
| `frontend/wailsjs/go/piplugin/Service.{js,d.ts}` | `wails generate module` 生成绑定 |
| `frontend/wailsjs/go/models.ts` | Wails 生成 `piplugin` models 命名空间 |

## 2. 逐项复审判定

| 原问题 | 判定 | 核验摘要 |
|---|---|---|
| P0-1 Wails Bind/生成绑定 | **已修复（静态链路）** | `main.go:63` 已 Bind；生成文件带 `DO NOT EDIT`，6 个后端方法齐全，前端 import 匹配。仍无真实 Wails 桌面交互证据。 |
| P1-1 Windows cmd 注入 | **已修复** | `internal/piplugin/service.go:94-116` 拒绝首轮指出的 `&|<>^%`，并额外拒绝 `()`；install/remove/update 均先走同一校验。测试覆盖三类 source 和 8 类攻击向量；仅缺 Windows 真实 `cmd.exe /c` L3。 |
| P1-2 package/runtime 目录脱节 | **已修复** | `app.go:191` 与 LaunchPiSession 同用 `pi-runtime`；`internal/piplugin/executor.go:31-35` 显式注入 agent dir，测试验证 env 与 argv。 |
| P1-3 增量 header 丢失 | **已修复** | `internal/appmeta/pi/parser.go:173-186` 每次从 0 预读 header，再 seek offset；两轮 fixture 断言 SessionID/ProjectDir 一致。 |
| P1-4 fork/clone 重复计费 | **部分修复** | 祖先文件存在时，`lineageRoot + entryID` 能让复制 entry 去重；同文件不同分支由 Pi 对全文件 ID 集合做碰撞检查，独立 entry ID 不同，parser 逐行计入。祖先文件缺失/移动后会回退当前文件，复制历史仍可重复，见 Major-1。 |
| P1-5 非 assistant usage 漏计 | **已修复** | assistant、带 usage 的 toolResult、compaction、branch_summary 四类均解析；fixture 对 4 条计费记录、tokens/cost/dedup 做断言。 |
| P1-6 Pi 原生成本币种 | **已修复** | native cost 在 parser 标 USD，`internal/usage/sync.go:616` 原样透传；零 native cost 才留给价格表。 |
| P1-7 headers 明文落盘 | **未修复** | `$ENV:` 解析和 0600 runtime 文件已实现，但正常 SaveProvider 会把 Headers/AuthHeader 整体清掉；literal header 仍被 resolver 原样接受，解析后的 secret 仍明文进入 runtime models.json。见 Major-2。 |
| P2-1 双根物理去重 | **已修复** | `internal/usage/sync.go:539-557` 按 EvalSymlinks 结果去重；alias 与 distinct-roots 测试映射成立。 |
| P2-4 npm pinned | **部分修复** | range/tag 已不再误标；但自写正则不等价于 Pi 使用的 `semver.valid`，非法 semver 仍可能被标 pinned，见 Minor-1。 |
| P2-5 截断尾行 | **已修复** | 仅 newline-terminated 行推进 committed offset；fixture 先写半行、后补齐并从旧 offset 正确恢复。 |
| P2-2 | **未纳入本轮** | 已另派前端，按任务契约不复审。 |
| P2-3 | **披露不修** | 与 fix-report 一致，不作为本轮新增问题。 |

## 3. 新发现与未闭环问题

### Major-1：lineage dedup 依赖祖先文件持续存在，祖先删除/移动后 fork 历史仍会重复计费

- 位置：`internal/appmeta/pi/parser.go:368-388,393-413`、`internal/appmeta/pi/parser.go:303`
- 影响：原 session 已入库后，如果其文件被删除/移动，应用重启后首次同步尚未入库的 fork，`EvalSymlinks(parentSession)` 失败，`resolveLineageRoot` 回退 fork 自身路径；复制 entry 的 key 与原记录不同，tokens/cost 再入库。Pi header 的 `parentSession` 是外部文件路径，不保证永久可访问。
- 证据：`walkLineageRoot` 对不可访问 parent 直接返回空；调用方将空 root 改为 `self`。当前 fork fixture `internal/appmeta/pi/parser_test.go:201-266` 只覆盖“祖先文件仍存在”。同理，跨文件但同 lineage 的独立 entry ID 只在各自文件内碰撞检查，极低概率的相同 8-hex ID 会被误去重；当前 key 没有事件指纹区分。
- 修复方向：dedup key 加入可跨复制稳定的事件指纹（排除 fork 时可能重写的 parentId），或持久化不可逆的 lineage identity；补“原记录已入库 → 删除祖先文件 → 同步 fork”DB 级 fixture，以及同 lineage 独立同 ID 不误去重 fixture。
- 分流建议：功能修复回 `luban`，dedup 语义如需调整由 `fuxi`确认。

### Major-2：P1-7 的 headers 保存链路被 scrub 整体丢弃，且 secret 策略只覆盖可选引用路径

- 位置：`internal/config/service.go:17-35,169-191,516-537`、`internal/config/types.go:247-266`、`internal/launcher/pi_config.go:83-93,149-170,175-211`
- 影响：
  1. 用户通过正常 SaveProvider 保存 `$ENV:` 引用、普通 header 或 `authHeader` 后，`scrubProviderAPIKeys` 重建 `AnthropicFormat/OpenAIFormat` 时没有复制 `Headers/AuthHeader`；字段随即从内存和 0644 配置消失，Pi 启动收不到 header，功能不可用。
  2. `resolveEnvHeaderValue` 对非引用 literal 原样返回；手工导入/既有 0644 配置仍可保存明文 secret。Build 时才解析 env，解析后的 secret 仍以明文写入 runtime `models.json`，只是权限收紧到 0600。
  3. `MkdirAll(agentDir, 0700)` 不会收紧已经存在的 0755 目录；当前权限测试只覆盖全新目录。
- 证据：`scrubProviderAPIKeys` 的两个结构体字面量只复制 Enabled/BaseURL/AuthKey/Organization，未复制新增字段；launcher 测试明确把 `"Bearer xyz"` 与 `"literal"` 作为合法透传，但没有 ConfigService save/load roundtrip。
- 修复方向：持久化层保留 `Headers/AuthHeader`，但对敏感 header 名强制 env/keychain 引用或拒绝 literal；明确 runtime 明文是受限落盘还是改为 Pi 支持的晚绑定方式；写入前显式 `Chmod(0700/0600)` 处理升级场景。补 SaveProvider→reload→BuildPiModelsConfig 全链路测试和旧 0755 目录升级测试。
- 分流建议：功能/安全回 `luban`，测试回 `wukong`。

### Minor-1：P2-4 的“精确 semver”正则与 Pi 官方语义不一致

- 位置：`internal/piplugin/source.go:257-263`、`internal/piplugin/security_pinned_test.go:48-63`
- 影响：正则会接受 `01.2.3`、`1.2.3-..` 等不满足 SemVer 的值并显示 pinned；Pi 当前实现使用 `semver.valid(version) !== null`。
- 证据：自写模式允许任意数字前导零，prerelease/build 段允许连续点；测试只覆盖 range/tag，没有非法 exact-looking case。
- 修复方向：使用成熟 SemVer 校验库或实现与 `semver.valid` 等价的严格规则，并补非法版本 fixture。

### Minor-2：重生成的 Wails models 文件引入 trailing whitespace

- 位置：`frontend/wailsjs/go/models.ts:1847,1852,1856,1883,1887,1914,1918,1947,1951,1975,1997,2001,2007`
- 影响：`git diff --check` 失败，污染生成 diff/质量门禁；无运行时影响。
- 证据：本轮实跑 `git diff --check` 退出码 2，命中以上 13 行。
- 修复方向：确认 Wails 生成器版本/模板是否固定产生该空白；在不手改生成文件的前提下统一生成版本或生成后执行项目认可的格式化流程。

## 4. 重点边界推理：P1-4

1. **同文件不同分支**：parser 不按当前 leaf 过滤，而是对四类计费 entry 逐行产出；dedup 为 `(lineageRoot, entryID)`。Pi 的 `generateId(byId)` 对该文件已存在的完整 ID map 做碰撞检查，因此同文件两个独立分支 entry ID 不同，均计入。
2. **fork/clone 复制**：祖先可访问时，fork header 的 `parentSession` 被递归解析到同一 canonical root，复制 entry 保留原 ID，因此 key 相同，DB `INSERT OR IGNORE` 不重复计费。
3. **边界失败**：祖先路径不可访问时 root 回退当前 fork 文件，复制 entry key 改变；跨 descendant 文件的新 entry ID 也没有 lineage 范围的碰撞检查。故本项只能判“部分修复”。

## 5. 测试证据审核

| 风险 | 测试映射 | 评价 |
|---|---|---|
| fork 复制历史 | `TestExtractUsageRecordsPiForkNoDoubleCount` | happy path 成立；缺祖先消失、DB 总量与跨 descendant ID 碰撞 |
| 截断尾行 | `TestExtractUsageRecordsPiTruncatedTail` | 映射完整，offset 与恢复记录均断言 |
| 增量 header | `TestExtractUsageRecordsPiIncrementalSessionContext` | 映射完整，两轮 session/project/offset 均断言 |
| Windows 拒绝 | `TestValidateSourceRejectsCmdMetachars` | 覆盖首轮指出的全部元字符与 npm/git/local；缺 Windows 真实 wrapper |
| `$ENV:` headers | launcher 三组测试 | resolver/runtime 权限覆盖；缺 ConfigService roundtrip 与升级权限 |
| 双根 alias | usage 两组测试 | alias 去重与独立根保留均覆盖 |

未发现明显凑数测试；主要问题是 P1-4/P1-7 的测试停在局部函数 happy path，没有覆盖真实持久化边界。

## 6. 本轮验证

工作目录：`/Users/maorun/maorun-workpace/amagi-codebox`

| 命令 | 结果 |
|---|---|
| `go build ./...` | PASS，退出码 0 |
| `go test -count=1 ./internal/piplugin/... ./internal/appmeta/pi/... ./internal/usage/... ./internal/remote/...` | PASS，4 个目标包组全绿 |
| `git diff --check` | FAIL，退出码 2；生成的 `frontend/wailsjs/go/models.ts` 有 13 处 trailing whitespace |

未执行真实 Wails 桌面交互或 Windows `cmd.exe`；因此 P0-1/P1-1 的运行时证据仍停留在静态链路与单测层。

## 7. 建议下一步

1. 回流 `luban` 处理 Major-1、Major-2；P1-7 必须补 ConfigService 持久化全链路测试。
2. `wukong` 补 fork 祖先缺失 DB fixture、同 lineage 独立 entry fixture、旧权限升级 fixture。
3. Leader 决定是否要求真实 Wails 桌面冒烟与 Windows CI 作为最终放行门槛。
4. 修复后仅复审上述增量；P2-2/P2-3 继续按既定分流处理。
