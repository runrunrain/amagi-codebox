# Pi coding agent 兼容性最终增量复审（R3）

## 结论

**不通过**。

原 R2 Major-1 的“祖先文件不可用时 fork 历史重复计费”已由内容指纹方案闭环；但 R2 Major-2 仅修复了 Headers/AuthHeader 被 scrub 丢弃的功能问题，敏感 literal header 仍会进入 0644 主配置，警告既不阻止落盘，也漏掉任意凭据型 header，并会把非法 `$ENV:` 前缀误判为安全引用。另有 3 个 Minor：指纹序列化存在可构造歧义且碰撞测试未覆盖精确残余风险、权限升级未在 Rename 前保证遗留 tmp 为 0600、手写 SemVer 缺少 npm semver 的长度和 MAX_SAFE_INTEGER 限制。

## 1. 增量 diff 基线

- 上游基线：`agent-outputs/pi-compat-review.md`（全量）与 `agent-outputs/pi-compat-review-r2.md`（R2 增量）。
- 修复声明：`agent-outputs/pi-compat-fix-r3-report.md`。
- Git 基线仍为 `HEAD 797f4907e43746950d6da2fe482995c0645e998b`；R2/R3 间没有独立 commit/index checkpoint，因此无法仅由 Git 重建 R3 patch。本报告只核验修复报告声明的 4 组增量及其测试，不重审其余累计工作区 diff。

### 1.1 本轮 changed files 与范围摘要

| 文件 | R3 变更范围 |
|---|---|
| `internal/appmeta/pi/parser.go` | `buildRecord` dedup 改为内容指纹；删除 lineage-root 解析/cache；更新风险说明 |
| `internal/appmeta/pi/parser_test.go` | 重写同 ID 碰撞与跨文件复制 fixture |
| `internal/config/service.go` | scrub 保留 `Headers/AuthHeader`；`SaveProvider` 增加敏感 literal header 警告 |
| `internal/config/service_test.go` | Save→reload→GetProvider 持久化回归 |
| `internal/launcher/pi_config.go` | 旧目录显式 0700；Rename 后显式 0600 |
| `internal/launcher/pi_config_test.go` | 0755/0644 升级权限 fixture |
| `internal/piplugin/source.go` | 手写严格 SemVer validator |
| `internal/piplugin/security_pinned_test.go` | exact-looking 非法版本 fixture |
| `frontend/wailsjs/go/models.ts` | 清除生成文件尾随空格；仅由 `git diff --check` 核验 |

## 2. 逐项判定

| R3 项目 | 判定 | 核验结论 |
|---|---|---|
| 1. Pi 内容指纹 dedup | **已修复** | 官方 Pi entry 都有 entry-level ISO timestamp；fork/clone 原样复制 entry，因此祖先存在与否都产生同一 key。mtime 只覆盖 malformed/外部污染数据，报告披露准确。原 Major-1 已闭环；但碰撞测试只部分映射统计残余，见 Minor-1。 |
| 2. scrub 保留 Headers/AuthHeader + warning | **部分修复** | Save/reload 功能恢复，APIKey 仍被 scrub；但 literal secret 仍落入 0644 主配置，warning 不是安全控制，且名字覆盖与 `$ENV:` 判定不完整，见 Major-1。 |
| 3. 0700/0600 升级权限 | **部分修复** | 旧目录和正常旧 models.json 会被收紧，失败路径在目录 Chmod 阶段会先停止；但遗留 0644 tmp 不会被 `os.WriteFile(...,0600)` 改权，Rename 后才 Chmod 存在窗口和失败后已提交问题，见 Minor-2。 |
| 4. 严格 SemVer | **部分修复** | core 前导零、空标识符、build、`v` 前缀、prerelease 数字前导零均与 npm semver.valid 对齐；但 npm 的 256 字符上限与 core 数字段 MAX_SAFE_INTEGER 未实现，见 Minor-3。 |

## 3. 重点推理

### 3.1 内容指纹与 fork/clone

- `internal/appmeta/pi/parser.go:304-305` 使用 `(entryID, occurredAt.UnixMilli(), model, provider, input, output, cacheRead, cacheWrite)`。
- Pi 官方 `docs/session-format.md` 将 `timestamp` 定义为所有 `SessionEntryBase` 的必填字段；当前 `session-manager.js` 的 append 路径均写入 `new Date().toISOString()`，fork/clone 原样复制非 header entries。因此：
  - 祖先文件存在：复制 entry 与原 entry 的选定字段相同，key 相同；
  - 祖先文件删除/移动：算法不再读取 lineage，key 仍相同；
  - `parentSession` 是否存在不参与 key，测试 `parser_test.go:299-307` 映射了该行为。
- `internal/appmeta/pi/parser.go:339-348` 的 mtime fallback 只会在 entry timestamp 缺失/非法时触发。对官方当前格式不是正常路径；对手工污染、异常旧数据仍可能让复制件不同 key并重复一次。R3 报告已明确披露，风险判断成立。
- 两个真实独立 entry 若恰好具有相同 32-bit entry ID、同毫秒、同 model/provider 与同四类 token，会被永久误去重一个。组合概率极低，但后果是该条 tokens/cost 被低估；这是内容复制 dedup 与独立同内容事件之间无法完全消除的统计取舍。

### 3.2 Headers/AuthHeader

- `internal/config/service.go:17-40` 正确保留两个格式的 `Headers/AuthHeader`，`service_test.go:2924-2971` 证明 Save→reload 后字段存在且 APIKey 为空。
- header 名使用 `strings.ToLower(strings.TrimSpace(k))`，大小写处理正确。
- `$ENV:` 判断只做 `HasPrefix`（`service.go:568`），与 launcher 的全串正则不等价：`$ENV:1bad`、`${ENV:KEY}suffix`、前后带空白的 `$ENV:KEY` 都会免警告，但 `resolveEnvHeaderValue` 不会解析，最终按 literal 处理。
- `AuthHeader` 是布尔开关，本身不携带 secret；保留它不新增明文凭据面。风险来自任意 `Headers` 值。

### 3.3 Chmod/Rename

- `internal/launcher/pi_config.go:153-159` 在写 secret 前先把 agentDir 收紧到 0700，正常升级顺序正确。
- fresh tmp 由 `os.WriteFile(...,0600)` 创建，Rename 继承其 inode/mode，正常路径不存在 0644 窗口。
- 但 `os.WriteFile` 的 mode 只在文件新建时生效。若旧版本/崩溃遗留 `models.json.tmp` 为 0644，本轮会在该 inode 上 truncate/write，仍保持 0644；`pi_config.go:172-178` 要到 Rename 后才 Chmod。实测 Go 行为：预建 0644 文件后 `os.WriteFile(path, ..., 0600)`，最终 mode 仍为 0644。
- 当前升级测试 `pi_config_test.go:108-142` 只预建 `models.json`，未预建 loose `.tmp`，因此没有覆盖该路径。

### 3.4 npm semver.valid 等价性

- 已对标 Pi 当前依赖 `semver@7.8.0` 的真实 `valid()`：
  - `01.2.3`、空 prerelease/build 标识符、`1.2.3-01`：双方均拒绝；
  - `1.2.3+01`、`v1.2.3`：双方均接受；
  - build 数字前导零允许、prerelease 数字前导零禁止：实现正确。
- 不等价处：npm `SemVer` 在解析前检查总长度 `MAX_LENGTH=256`，并拒绝 core major/minor/patch 大于 `Number.MAX_SAFE_INTEGER`。当前 `source.go:262-291` 没有两项限制，会把 `9007199254740992.0.0` 和超长合法形状字符串标为 pinned，而 npm `semver.valid()` 返回 `null`。

## 4. 未闭环与新发现

### Major-1【安全，置信度 98%】：敏感 header 仍明文保存，warning 覆盖不完整且可误判非法 env 引用

- 位置：`internal/config/service.go:17-40,160,195,545,550-579`；`internal/launcher/pi_config.go:183-205`
- 影响：`Headers` 是任意 map。除四个名字外，`Cookie`、`X-Auth-Token`、`X-Access-Token`、`X-Goog-Api-Key`、供应商 subscription key 等都可携带凭据，却无警告地进入 0644 主配置；四个已识别名字的 literal 也只是警告后照常落盘。配置导出/备份或本机其他可读主体仍可取得凭据。
- 证据：主配置两条写盘路径仍为 0644；`warnSensitiveLiteralHeaders` 只列四个名字、不拒绝保存；prefix 判断与 launcher exact regex 不一致。新增测试仅证明字段保留，没有断言 warning、非法 env ref 或其他凭据 header。
- 修复方向：对已知敏感名和可配置 credential header 强制 exact env/keychain reference，或将 secret value 从普通配置分离；warning 应复用与 launcher 完全一致的“整串合法引用”判定。若产品决定允许 literal，至少主配置需 0600、导出需 scrub，并把残余风险明确作为产品安全决策，而不能标“已修复”。
- 分流建议：`luban`；安全策略如需变更由 Leader 决定。

### Minor-1：内容指纹输入无边界编码，碰撞测试未覆盖精确残余风险

- 位置：`internal/appmeta/pi/parser.go:304-305,386-391`；`internal/appmeta/pi/parser_test.go:259-307`
- 影响：`fmt.Fprint(h, parts...)` 无长度前缀/分隔符，两个不同 tuple 可构造同一预哈希字节串。例如同 ID/时间/tokens 下 `(model="ab", provider="c")` 与 `(model="a", provider="bc")` 得到相同 hash16；该条 usage 会被误去重。另一个不可消除的残余是两个真实独立 entry 的所有选定字段完全相同。
- 证据：最小抽检得到上述两组输入 hash16 均为 `8058813bffbe6296`。现有碰撞 fixture 同时改变 timestamp 和 input，只证明“明显不同字段会不同”；未覆盖字段边界歧义、只变内容/cost/totalTokens 或 exact selected tuple 的风险。
- 修复方向：使用长度前缀或结构化编码后再 hash，并考虑纳入 entry type、`totalTokens`、native cost/完整 message 内容摘要；测试名称不要宣称绝对 collision-safe，应显式记录统计取舍。

### Minor-2：权限升级未在 Rename 前保证 tmp 为 0600

- 位置：`internal/launcher/pi_config.go:167-178`；`internal/launcher/pi_config_test.go:108-142`
- 影响：遗留 0644 `.tmp` 会以宽松 mode 接收新的敏感内容；Rename 后 Chmod 之间存在窗口，若 Chmod 失败则函数已提交新文件后才报错。目录已先收紧为 0700，降低了常规跨账号利用面，但不能撤销在收紧前已打开的 loose tmp 文件描述符。
- 证据：Go 实测现有 0644 文件经 `os.WriteFile(...,0600)` 后仍为 0644；当前测试未覆盖 stale tmp。
- 修复方向：使用唯一临时文件并在写入/rename 前显式 `Chmod(tmp,0600)`，失败则删除 tmp 且不得 commit；增加 loose stale tmp 升级 fixture。

### Minor-3：手写 SemVer 未完全等价 npm semver.valid

- 位置：`internal/piplugin/source.go:257-321`；`internal/piplugin/security_pinned_test.go:51-69`
- 影响：npm 自身判 invalid 的超大 core 数字或超长版本会在 UI 被误标 pinned。
- 证据：Pi 使用的 `semver@7.8.0` 对 `9007199254740992.0.0` 返回 `null`；当前 Go validator 只检查纯数字与前导零，会返回 true。npm 同时有 256 字符总长限制，当前实现没有。
- 修复方向：补 `MAX_SAFE_INTEGER` 与 256 字符限制，并加入真实 npm 对照 fixture；其余本轮指定语义无需改动。

## 5. 测试证据审核

| 风险 | 当前测试 | 评价 |
|---|---|---|
| fork 祖先存在/不可解析 | `TestExtractUsageRecordsPiForkNoDoubleCount`、`TestExtractUsageRecordsPiDedupCollisionSafe` | 核心修复映射成立；collision-safe 边界覆盖不完整 |
| Save/reload headers | `TestSaveProviderRetainsHeadersAndAuthHeader` | 功能 roundtrip 成立；未覆盖 warning 与敏感 literal 策略 |
| 旧权限升级 | `TestWritePiAgentConfigUpgradesLegacyPerms` | 正常旧 dir/file 成立；缺 loose stale tmp |
| strict SemVer | `TestIsExactSemver` | 用户点名的前导零/空段/build/v/prerelease 已覆盖；缺 npm 实现上限 |

未发现凑数测试，但三处测试名称/注释把局部 happy path 描述成完整等价或完整安全，证据强度不足以支持对应结论。

## 6. 实跑验证

工作目录：`/Users/maorun/maorun-workpace/amagi-codebox`

| 命令 | 结果 |
|---|---|
| `go build ./...` | PASS，退出码 0 |
| `go test -count=1 ./internal/config/ ./internal/launcher/ ./internal/piplugin/ ./internal/appmeta/pi/ ./internal/usage/` | PASS，5 个目标包全绿 |
| `git diff --check` | PASS，退出码 0；R2 Minor-2 已修复 |
| Pi bundled `semver@7.8.0` 对照抽检 | PASS（完成对照）；确认 `9007199254740992.0.0` 与超长版本为 invalid |
| hash 输入边界最小抽检 | 复现；不同 `(model,provider)` tuple 得到同一 hash16 |

本轮无前端行为变更，不触发 agent-browser；未重审既有 Wails L3 缺口。

## 7. 建议下一步

1. 先由 Leader 决定敏感 literal header 的正式产品策略；若继续普通配置明文落盘，本轮不能视为修复 R2 Major-2。
2. 回流修复 Major-1，并最小补齐 warning exact-ref/常见 credential header 测试。
3. 同批处理 loose tmp pre-rename 权限与 SemVer 两个边界；dedup 的统计取舍可由 Leader决定是修实现还是明确接受并收窄测试命名。
4. 任一代码再变更后，只复审对应增量。
