# P3-S4 报告：Claude/Codex 用户配置导入 + OpenCode 附加层扩展

> 摩擦批次 P3-S4 · HEAD 基线 4614efd · 本轮工作目录 X:\WorkSpace\amagi-codebox。
> 上游：p3-config-supply-design.md §四（Claude/Codex/OpenCode 表逐行）、§五（转换器复用）、§八（S4 清单）；S2 键级投影/同意门/转换器全表与 S3 资源目录单元全部复用。

## 一、交付映射（S4 范围 → 产物）

| S4 范围 | 产物 | 说明 |
|---|---|---|
| 1a Claude `~/.claude.json` mcpServers | `internal/wslsupply/claude_adapter.go`（单元 `claude/mcp`） | server 级键级投影；**仅用户选择集内的 server 导入**（`StateEntry.SelectedKeys` 持久选择门）；复用 S2 `resolveKeyed` 冲突框架（unmanaged-existing/手改冻结/双侧冲突/来源删除=取消选择移除）与 `TransformMCPServer` 确定规则；projects/trust/onboarding/账户状态/未知键 LocalOnly（desired 只从 mcpServers 抽取，永不以「未排除」携带） |
| 1b Claude `~/.claude/settings.json` | 同文件（单元 `claude/settings`） | 字段级白名单（model/outputStyle/spinnerTipsEnabled/cleanupPeriodDays/includeCoAuthoredBy——仅跨平台标量偏好）；permissions/hooks/env/未知字段不在白名单=永不导入（「设置可能授权执行」红线） |
| 1c CLAUDE.md/commands/agents/skills | `internal/wslsupply/resources.go`（`ResourceScope`，单元 `claude/res/<rel>`） | S3 目录单元机制推广：有界扫描、静态兼容检查（越界/绝对/循环链接、Windows 专用脚本）、per-target 同意门、TrackedProjection 冻结/CAS/staging 提交/条件撤销全链复用（`planTreeUnitCore`/`submitDirUnit` 与 skills 共享实现） |
| 2a Codex `config.toml` | `internal/wslsupply/toml_edit.go` + `codex_adapter.go`（单元 `codex/config`） | mcp_servers/<name> + 选中 profiles/<name> TOML 节投影；**TOML 保真行级编辑**（见下「关键取舍」）；shim/路径转换复用 §五全表；候选自校验（重新解析 + 受管段值 round-trip 相等才提交） |
| 2b Codex AGENTS.md/用户 skills | `resources.go`（`codex/res/<rel>`，目标根 `.codex`） | 同 1c |
| 3 OpenCode 附加层扩展 | `internal/launcher/wsl_opencode_mcp.go`（`AdaptWSLOpenCodeMCPWithOptions` + `PreviewWSLOpenCodeMCP`）+ `app_wsl_supply_s4.go` 选择存储 | 附加层机制保留；**同名冲突默认保留本地 + 显式覆盖选择**（`~/.amagi-codebox/wsl-opencode-mcp.json`，0600 原子写，per-distro）；S1 全量形态检查语义不变（`allowed=nil` 兼容既有调用方） |
| 4 导入预览与状态面 | `app_wsl_supply_s4.go`（13 个 App 方法）+ `frontend/src/api/wslSupply.ts` + `WSLCLISettings.vue` 两新区块 + wailsjs | 每 CLI 独立状态卡（键级单元 + 资源扫描）；预览含**转换前→转换后脱敏对照**（Before/After：命令形态与参数个数，不含 env 值/密钥；事实经显式预览动作实测）与选择勾选面；五动作复用 S2 框架（预览/导入所选/应用安全更新/保留本地/确认接管） |

明确不做（按任务书）：插件（S5）、B5、OpenCode 全局文件镜像（否决项）——未触碰。

## 二、独立验收（§八 S4 清单 → 证据）

| # | 验收 | 证据 | 状态 |
|---|---|---|---|
| 1 | 每 CLI ≥1 远程 MCP + 1 Linux stdio MCP 真实启动 | `internal/wslsupply/s4_e2e_windows_test.go::TestManualRealWSLS4UserConfigImport`（AMAGI_WSL_E2E=1，真实 distro "Ubuntu"，隔离 home `/tmp/amagi-wslsupply-s4-e2e`）：claude 子测试导入 `web-remote`(url) + `stdio-native`(npx.cmd→**npx，经真实登录 shell command -v 实证映射**)；codex 子测试同对 + distro 既有注释/沙箱节字节保留。**降级说明（如实）**：该 distro 未装 claude/codex CLI，未做 CLI 二进制实际解析；按任务书降级条款执行「配置 schema 校验（JSON 可解析/受管段 round-trip）+ 消费者入口检查（文件位于 CLI 消费路径）」，测试内输出明确降级文案。运行记录：2026-10-16 本机，PASS（连同 S1/S3 既有 e2e 全绿） | ✅（含降级） |
| 2 | Windows 信任/凭据/历史不进 distro | 负面断言测试：`claude_config_test.go`（projects/hasCompletedOnboarding/oauthAccount 断言不跨界；目标已有用户文件时 unmanaged 冻结且逐字节不动）；`codex_config_test.go`（Windows 侧 projects/trust_level/`# 用户注释` 断言不落 distro；profile 携带 Windows 路径→整节不导入）；e2e 两个子测试均断言 distro 侧无 auth/credential/history/session 文件（`s4E2EAssertNoCredentialFiles`） | ✅ |
| 3 | TOML 非目标节与注释保留 | `toml_edit_test.go`：零编辑 round-trip 字节恒等；删除/替换/追加 mcp_servers 后非受管内容（含表前注释、行尾注释、`[projects]`/`[sandbox]`、CRLF 风格、多行数组、点键形态）逐字节保留；`codex_config_test.go` 端到端（distro 预置注释文件 + 导入 + 取消选择删除后其余字节不动）；候选自校验 `verifyTOMLCandidate`；e2e 真机复验 | ✅ |
| 4 | 全局与附加层同名冲突遵循选择 | `internal/launcher/wsl_opencode_mcp_s4_test.go`：默认未勾选→同名 server 保留本地不写入（报告行）；勾选 overrideGlobal→写入且 shim 映射为原生命令；allowed 过滤门与 nil 兼容语义钉住；`app_wsl_supply_s4_test.go` 选择存储 round-trip（排序、去空、未登记 distro 返 nil） | ✅ |
| 5 | 所有 CLI 未知字段保持本地 | pi：既有 `TestPiSettingsProjectionLifecycle`（packages/机器字段不携带）；claude：验收 2 证据 + settings 白名单外字段断言；codex：未知节/未知命令（setup.bat）不导入断言。机制性保障：desired 一律从白名单/mcpServers/受管 TOML 作用域抽取 | ✅ |
| 6 | build/vet/gofmt + 改动包测试 + 前端 + wailsjs | 见「三、验证记录」 | ✅ |

## 三、验证记录（真实命令与结果）

命令均以 `go.exe`（Windows Go 1.25.5，经 WSL interop 调用，`WSLENV` 转发 GOCACHE/GOPATH）在 `X:\WorkSpace\amagi-codebox` 执行：

- `go build ./...` → exit 0。
- `go vet ./internal/... .` → exit 0（无输出）。
- `gofmt -l`（全部改动文件）→ 清洁（已 `-w` 归一）。
- `go test ./internal/wslsupply/ -count=1` → ok（含 S4 新测试 27 个 + S0–S3 全量回归）。
- `go test ./internal/launcher/ -run OpenCode -count=1` → ok。
- `go test ./internal/platform/ -run WSL -count=1` → ok（`--exec` 修复后）。
- `go test . -run "WSL|Supply|OpenCodeMCP|S4" -count=1` → ok（根包 WSL/S4 面）。
- e2e：`AMAGI_WSL_E2E=1 go test ./internal/wslsupply/ -run TestManualRealWSL -v -count=1` → S1 managed-models / S1 vision / S3 skills / **S4 user-config** / supply-target 全 PASS。
- 前端：`npm run build`（vue-tsc + vite）→ 成功；`npx vitest run` → **27 文件 / 241 测试全过**（含 wslSupply.test.ts 新增 S4 断言 19 个中的新增部分）。
- wailsjs：`frontend/wailsjs/go/main/App.js`/`App.d.ts`/`models.ts` 按 wails 生成格式更新（13 个新方法 + 2 个新模型 + KeyPreview 扩展字段）。**说明**：本机无 wails CLI（未安装、vendor 内无 cmd/wails），沿用 S2/S3 批次先例按生成器输出格式精确补齐；下次 `wails dev/build` 再生成时格式应幂等。

**环境性基线失败（与本次改动无关，已取证）**：在 HEAD=4614efd 的独立 worktree 复跑同样失败——根包 `TestAppConfigEventOnUserChange`/`TestSetRemoteEndpoint_*`/`TestOpenRemoteWebUI_*`（Windows TempDir 日志句柄锁导致 RemoveAll 清理失败）；`TestR6_001_*`/`TestR10_001_*` 等（`exec: "sleep": executable file not found`——测试假定 Unix PATH）；`TestResolveLaunchWorkDirChain`（X:/C: 跨盘 Rel）；`internal/config` 与 `internal/launcher` 的 perm 断言（`mode=666 want 0600`——Windows 无 POSIX chmod；CI 对 Windows 仅 `-run '^$'` 编译检查，全量测试在 macos-latest）；`internal/platform` 的 fnm symlink 用例（无 SeCreateSymbolicLink 特权）。

## 四、关键取舍与写面外修复（透明声明）

1. **TOML 保真取舍（任务书授权的最小侵入方案）**：仓库未 vendored TOML 库，全量 parse-rewrite 无法保注释。实现为「定位行级替换」：仅解析受管作用域（mcp_servers.*/profiles.*）的表头/键值行，其余字节逐字保留；受管域内不支持形态（数组表、裸表直接键、顶层作用域赋值、点引号段、日期时间值、多行字符串）→ 单元受阻不改写文件（design「解析失败停止该文件」）。首批只开放有测试的节，不宣称任意 config.toml 可无损转译。
2. **敏感度分类**：`.claude.json` 与 `config.toml` 携带大量用户自有状态——按普通单元处置（chmod 补偿失败恢复前像，不整文件删除；新文件场景清理半提交），密钥副本不在宽松权限下新落盘。pi/mcp.json 的 S2 分类（sensitive=true）不变。
3. **OpenCode 选择门语义**：依据 design §四原文「仅作用于本次显式选择的 **preset**/已授权导入项」，preset 本身即显式选择（启动面板选定）；本批引入的逐项门是**全局同名冲突的显式覆盖**（默认保留本地），未引入逐项导入开关（allowed 参数保留供未来使用，生产传 nil 不回归 S1 行为）。
4. **【写面外最小修复·internal/platform/wsl_home.go】**：`wslScriptRunner` 由 `wsl.exe -- sh -lc` 改为 `--exec sh -lc`。根因（实测取证）：`--` 路径会把 argv 重拼后经中间 shell 再解析，脚本文本中的 `"$c"`/`"$p"` 被中间 shell 展开为空——`WSLResolveCommands`（S1 起生产裸 shim 验证）与 `WSLTranslatePaths`（S2 起路径实测映射）**在真机上恒返回空**（保守拒绝方向，安全但错误）；`--exec` 逐字传参后真机实证解析到 `/usr/bin/npx`。该修复是本批验收 1 的必要条件；全部既有探测调用方（identity/home/stat/chmod/search-tools）在修复后真机 e2e 复验通过（S1/S3/S4 e2e 全绿）。改动一行+注释，最小范围。
5. **【顺手修复·coordinator.go 跨代基线泄漏】**：`recordProjectionPendingConsent`/`recordProjectionFreeze` 原样复制 prev.Subs 不分代际——distro 重装后 awaiting-consent 登记会把旧代键级基准带进新代（重新同意后首发布被误判 local-deletion-frozen）。该缺陷此前被 GrantUnitConsent 不保留 Subs 的行为掩盖；本批 GrantUnitConsent 为保留选择集而同代保留 Subs 后暴露。已按 F3 语义修复为仅同代保留，`TestPiConfigUnitsRequireConsent/cross-generation` 钉住。

## 五、未覆盖项 / 风险 / 盲区

- **CLI 二进制实际解析未验证**（验收 1 降级）：隔离 distro 未装 claude/codex；已装 CLI 的真机（`claude mcp list` / `codex mcp list` 实际消费配置）留待有 CLI 的环境补做。
- 行级 TOML 编辑器对极端形态（受管段与点键混排同名、`[[mcp_servers.x]]` 数组表）一律受阻不编辑——合法保守，但用户需手动适配这些文件。
- 预览的转换前→转换后「后」侧对 shim 映射依赖当次事实探测（显式预览允许唤醒目标）；探测失败时显示「转换需实测验证」而非假结果。
- wailsjs 为手工按生成器格式补齐（本机无 wails CLI）；下轮 `wails build` 需确认幂等。
- 前端交互为类型检查 + 单测覆盖，**未做浏览器人工点验**（视觉/交互验收属洛神面，未宣称）。
- 工作区既有未跟踪文件 `app_wsl_skills_test.go`（S3 遗留，非本批产物）未纳入、未改动；`frontend/C:\...` node 编译缓存垃圾目录（任务书点名对象）已物理移除至 /tmp 暂存（本批不 commit）。

## 六、回滚方式

- 禁用各适配器即回退 S4 面：`registerS4Adapters`（app_wsl_supply.go）移除注册即可（unknown unit 自动降级，无残留写入）；已导入条目按单元 hash 条件撤销（CAS，用户后改不动），不全量恢复用户根。
- OpenCode 覆盖选择：删除 `~/.amagi-codebox/wsl-opencode-mcp.json` 即回 S1 默认（同名保留本地）。
- platform `--exec` 修复如需回退：还原 `wsl_home.go` 该行为（会恢复 S1 起即存在的探测恒空缺陷，不建议）。

## 七、文件清单（本批变更/新增）

新增：`internal/wslsupply/{claude_adapter,codex_adapter,toml_edit,resources}.go` + 5 个测试文件（claude/codex/toml/resources/s4_e2e）；`internal/launcher/wsl_opencode_mcp_s4_test.go`；`app_wsl_supply_s4.go` + `app_wsl_supply_s4_test.go`。
修改：`internal/wslsupply/{types,projection,pi_config,skills,skills_submit,coordinator,state}.go`；`internal/launcher/wsl_opencode_mcp.go`；`internal/platform/wsl_home.go`（§四.4 写面外修复）；`app_wsl_supply.go`；`frontend/src/api/wslSupply.ts`、`frontend/src/views/settings/WSLCLISettings.vue`、`frontend/src/__tests__/api/wslSupply.test.ts`、`frontend/wailsjs/go/main/{App.js,App.d.ts}`、`frontend/wailsjs/go/models.ts`。

**建议下一步**：在装有 claude/codex CLI 的 distro 补一次真实 `mcp list` 解析冒烟（关闭验收 1 降级项）；platform `--exec` 修复建议知会 S1–S3 负责人（其真机行为由恒空探测变为真实探测，shim 映射与路径映射首次真正生效）。

---

## 八、S4 diting quick 审 FIX 修复轮

> 审阅来源：diting quick 审（4 项：2 Major + 2 Minor）。注：审阅报告文件（s4-quickreview-diting.md 或同名变体）未出现在 agent-outputs/workflow/wsl-session-friction/ 目录（该目录仅有 s0/s1/s2/s3 的审阅件与 s4-report.md）；修复以任务书内嵌的完整 FIX 清单（含文件:行锚点）为准。

### F1（Major）wailsjs App.js 字面量传参 — 已修

- 根因：首轮用脚本生成 S4 方法时把调用侧参数写成了 `('arg1')` 字符串字面量而非变量 `(arg1, arg2)`——共 9 处调用（ApplyWSLCliConfigSupply / ApplyWSLCliResourcesSupply / CheckWSLCliResourcesSupply / GetWSLCliSupplyStatus / ImportWSLCliConfigKeys / KeepWSLCliConfigUnitLocal / KeepWSLCliResourceLocal / PreviewWSLCliConfigSupply / PreviewWSLCliResourcesSupply / SetWSLOpenCodeMCPSelection，另 GetWSLOpenCodeMCPPreview 无参不受影响；末尾两个 TakeOver 由 edit 工具添加本就正确）。全部改为变量透传（与同文件既有生成方法同形态）。
- 防再犯：新增 `frontend/src/__tests__/wailsjs-s4-args.spy.test.ts`——真实执行 wailsjs App.js 函数体（安装 window['go']['main']['App'] spy），对 13 个 S4 方法逐一断言「调用方变量实参原样到达绑定层」。杀伤力实证：临时回注 1 处字面量传参 → 该测试 1 failed；复原后 13 全过。vue-tsc（只看 d.ts）与 api 层 vitest（mock 绑定模块）均不覆盖此层，本测试补齐。

### F2（Major）verifyTOMLCandidate 范围越界 — 已修

- 根因：候选自校验对 subs 全量断言 Written 恒等——冻结/保留本地键携带陈旧基准（目标侧为用户手改值）必然 drift → 整候选被拒：健康键更新无法落盘、adopt「来源已删+目标手改保留本地」永远失败。
- 修复：`buildCandidate` 采集本轮实际写入/更新的键集（`writtenThisRound`）与被移除键集（`removedThisRound`）；`verifyTOMLCandidate(content, written, removed)` 只校验写入键（段存在+值与写入基准一致）与移除键（段不存在）。陈旧基准键不在本轮编辑面内，不参与校验。
- 新测试①：`TestCodexFrozenKeyDoesNotVetoHealthyUpdate`——web 手改冻结 + fetch 来源更新共存 → applied+partial，fetch 新参数落盘、web 保留本地值、旧参数被替换。
- 新测试②：`TestCodexAdoptKeepsHandModifiedStaleSection`——来源删除 web + 目标手改 + fast 变化 → 接管成功（takeover-written）、「保留本地」note、web 保留手改值、fast 按来源覆写。

### F3（Minor）根级点键内联表误导诊断 — 已修

- 原状：`mcp_servers.x = { … }` 形态经 SectionValue 重建时空路径丢弃 → 段值 `{}` → 下游「无命令/不是对象」误导文案。
- 修复：SectionValue 对 len(full)==2 的根级点键取内联表值为段本体（各键并入）；解析层对根级点键赋非表值（如 `mcp_servers.x = "scalar"`）按 `errTomlUnsupportedForm` 明确拒绝。
- 新用例：`TestTOMLEditRootDottedInlineTable`（内联表段值正确 + 非表值拒绝并点名不支持形态）。

### F4（Minor）断言强度升级 — 已修

- `toml_edit_test.go`：删除/新增/替换三用例的非受管保真从片段 Contains 升级为整文确定性期望串全文相等（afterRemove/afterAppend/afterReplace 常量）。零编辑 round-trip 字节恒等保留。
- `codex_config_test.go::TestCodexConfigSelectionAndTOMLFidelity`：导入结果与取消选择结果均升级为整文确定性期望（distro 预置内容逐字节 + 受管段按判定序追加/移除的精确形态）；原片段 Contains 断言被全文相等覆盖后移除。

### 修复轮验证记录

- `go build ./...` ✓；`go vet ./internal/... .` ✓；`gofmt -l`（改动包/文件）✓。
- `go test ./internal/wslsupply/ -count=1` → ok（含 F2/F3/F4 新测试）。`go test . -run "WSL|Supply|OpenCodeMCP|S4" -count=1` → ok。
- `go test ./internal/launcher/ -count=1`：除 4 个**预存在** Windows 权限位环境失败（TestWriteOmpAgentConfigTightPerms/UpgradesLegacyPerms、TestWritePiAgentConfigTightPerms/UpgradesLegacyPerms——`perm=666 want 0600`，文件不在本两轮改动面，CI 仅在 macos-latest 跑全量、Windows `-run '^$'`）外全绿；OpenCode 面（本批改动）全绿。
- 前端：`npm run build` ✓；`npx vitest run` → 28 文件 / 254 测试全过（含 F1 spy 新增 13 个）。
- 提交前核查：`frontend/` 无 `C:` 垃圾目录 ✓；未 commit/push。
