# P1 批次一交付报告：WSL 会话环境供给快赢组（T4+T12+T13+T6/B3）

- 任务：实现 WSL 会话摩擦治理证据报告中的 4 条快赢项（纵向切片）
- 上游证据：`agent-outputs/wsl-session-friction/evidence-report.md`（17 条）、`toolcall-evidence.md`（14 条）
- 执行环境：Windows 宿主（DESKTOP-NAALPFC，WSL2 Ubuntu）+ Windows go1.25.5（经 WSL 互操作调用，`GOCACHE` 经 WSLENV 透传）
- 未 commit/push（Leader 统一提交）

## 一、逐条改动说明

### 1. T4：opencode WSL 会话配置可达（方案 A，已验证后选定）

**调研结论（方案选择依据）**：本机安装的 opencode `v1.17.18`（`C:\Users\…\npm\node_modules\opencode-ai`）二进制反查（grep 字符串上下文）确认三个 env 语义：
- `OPENCODE_CONFIG`：附加配置文件路径。加载链为 `全局配置(r.Path.config) → OPENCODE_CONFIG 自定义层 → 项目级 opencode.json → OPENCODE_CONFIG_CONTENT`（后层覆盖前层）。它是**附加层而非顶替层**——指向独立文件不会顶掉用户自己的 `~/.config/opencode/opencode.json` 全局配置。
- `OPENCODE_CONFIG_CONTENT`：内联 JSON，"local" 源、最高优先（Windows 侧现状载体）。
- 三者任一存在时跳过全局 `$schema` 脚手架写入。

**选方案 A 的理由**：方案 A 把配置落到 distro 内文件（持久、无 32K 环境变量上限风险），`OPENCODE_CONFIG` 值为 Linux 路径（`isWindowsDrivePath` 判据天然不剥离）；方案 B 转发 CONTENT 则受 Windows 环境变量单值 ~32K 上限约束，且大 JSON 进 WSLENV 转发面无谓膨胀。二进制证据确认 OPENCODE_CONFIG 可用，故 A 成立。

**实现**：
- `internal/launcher/wsl_agent_config.go`：新增 `WriteWSLOpenCodeAgentConfig(distro, cfg)`——复用 `resolveWSLAgentTarget(distro, ".config/opencode")` 与 `wslAgentConfigMu` 互斥，原子写（MkdirAll→MarshalIndent→tmp→Rename）`~/.config/opencode/amagi-codebox.json`（CodeBox 命名空间私有名，**不碰用户全局 opencode.json**；整体覆盖、无 merge——与 pi/omp 的差异及理由见函数注释），chmod 700/600 补偿失败即清理落盘明文（fail closed，对齐 P1-7 契约）。返回 Linux 文件路径。
- `internal/platform/wsl.go`：`wslENVForwardExact` 增加 `OPENCODE_CONFIG`（原样转发，无 /p 翻译标志——值已是 distro 内路径）。
- `app.go` `LaunchOpenCode` 内嵌分支：WSL 模式（`EmbeddedLaunchTargetsWSL` + `DefaultWSLDistro`）下，把 `envOverrides["OPENCODE_CONFIG_CONTENT"]` 反序列化为 runtime config → 写 distro → `OPENCODE_CONFIG=<linux path>` 注入 env（**在 `resolveEmbeddedLaunchSpec` 之前**，确保 resolver 拼 WSLENV 时能看到该键）。无 distro 时 WARN（对齐 pi 的 B6 退化提示）；写失败记 ERROR 不阻断启动（回退现状＝无 provider，与 pi 的 WSL 写入失败策略一致）。
- 双轨（opencode_presets 新模型 / terminal_presets 旧模型）产物都汇入 `envOverrides["OPENCODE_CONFIG_CONTENT"]`，注入点在两轨汇合后的 embedded 分支，两轨同时受益；`presetName == ""`（全局配置模式）无 CONTENT，不写入不注入。

### 2. T12：DrvFS 慢速告警识别 /mnt/* 形态

- `internal/pty/service.go`：`workDirMapsToDrvFS` 扩展识别 `/mnt/<单盘符>` 形态（`/mnt/d`、`/mnt/c/repo`；只匹配单字符盘符段，`/mnt/wsl`、`/mnt/docker-desktop`、`/mnt/usb` 等多字母段不命中）。`drvfsMountPath` 对 Linux 形态原样返回（已在挂载点上），告警日志明细正确携带。

### 3. T13：WSL 会话注入 LANG

- 先读 darwin 实现（`service_darwin.go` 的 `buildDarwinPTYEnvironmentFromBase`）：确认语义是 **LANG/LC_ALL/LC_CTYPE 三者任一存在即不注入**（已存在不覆盖），注入值 `LANG=en_US.UTF-8`。
- `internal/pty/service.go`：新增 `ensureWSLUTF8LocaleEnv`——条件与 darwin 逐字对齐；因变量跨界只经 WSLENV 白名单，注入同时把 `LANG` 追加进 WSLENV 清单（已有 WSLENV 原样保留，清单已有 LANG——含 `/flags` 形态——不重复追加）。挂接点：`StartResolvedWithRunEvidence` 在 `conpty.ConPtyEnv` 之前、仅 `BootstrapWSL` 分支生效。

### 4. T6/B3：Windows 路径值环境变量剥离泛化

- `internal/launcher/wsl_agent_config.go`：`StripWSLHostPathPIEnv` 改名泛化为 `StripWSLHostPathEnv(env, extraForwardKeys...)`，返回 `(kept, strippedKeys)`；判据经新增导出的 `platform.ShouldForwardToWSL(key, extras...)`（与 `appendWSLENVForwarding` 拼清单完全一致：五前缀 + 精确键 + envvars.json 自定义键，保留键除外）。标量值、Linux 路径值（含 T4 注入的 OPENCODE_CONFIG）、不在转发面的键（如 USERPROFILE）不受影响。
- **调用点重构**：剥离从 pi/omp 两处原地调用上移到 `resolveEmbeddedLaunchSpec`（app.go:2360+）——全部五 CLI 内嵌启动的单一漏斗，claude/codex（T6 的 CODEX_HOME 实例）与 opencode 一并覆盖；剥离命中记 WARN（可观测性，旧实现无日志，本次补齐）。remote/v1 重启路径若经同一漏斗亦自动受益。
- `docs/developer/architecture.md:202`：函数改名一行同步。

## 二、文件清单

| 文件 | 改动 |
|---|---|
| `internal/platform/wsl.go` | wslENVForwardExact +OPENCODE_CONFIG；导出 ShouldForwardToWSL（含 extras）；shouldForwardToWSL 变薄包装 |
| `internal/launcher/wsl_agent_config.go` | +WriteWSLOpenCodeAgentConfig/writeOpenCodeConfigFile/openCodeWSLConfigFile；StripWSLHostPathPIEnv→StripWSLHostPathEnv 泛化；头注释扩充；import +encoding/json |
| `internal/pty/service.go` | workDirMapsToDrvFS/isLinuxDrvFSMount/drvfsMountPath 扩展；+ensureWSLUTF8LocaleEnv/hasEnvEntry/envEntryValue/setEnvEntry；StartResolvedWithRunEvidence WSL 分支挂接 |
| `app.go` | resolveEmbeddedLaunchSpec 统一剥离漏斗；pi/omp 旧剥离移除；LaunchOpenCode WSL 配置注入 |
| `docs/developer/architecture.md` | 改名同步（1 行） |
| `internal/platform/wsl_test.go` | +TestShouldForwardToWSLExtraKeys、TestAppendWSLENVForwardingOpenCodeConfig；TestShouldForwardToWSL 增 OPENCODE_CONFIG |
| `internal/launcher/wsl_agent_config_test.go` | TestStripWSLHostPathPIEnv→TestStripWSLHostPathEnv（全前缀+自定义键覆盖）；+3 个 WriteWSLOpenCodeAgentConfig 测试 |
| `internal/launcher/wsl_agent_e2e_windows_test.go` | +TestManualRealWSLWriteOpenCodeConfig（快照/恢复）+ 辅助函数 |
| `internal/pty/wsl_drvfs_test.go` | TestWorkDirMapsToDrvFS/TestDrvfsMountPath 增 /mnt 用例；告警测试增 /mnt/d 命中与 /mnt/wsl 排除段 |
| `internal/pty/wsl_env_test.go`（新） | 4 个 ensureWSLUTF8LocaleEnv 测试 |

## 三、自验证据（每条摩擦 → 修复 hunk → 验证）

### 构建/静态检查（真实命令，Windows go1.25.5）
```
go build ./...                 → exit 0
go vet ./internal/platform/ ./internal/launcher/ ./internal/pty/ .  → exit 0
gofmt -l <9 个改动文件>        → 空（全部已格式化）
```

### T4（hunk：wsl.go:266-285；wsl_agent_config.go:176-259；app.go:5280-5307）
- 单测：`TestAppendWSLENVForwardingOpenCodeConfig`（OPENCODE_CONFIG 进 WSLENV 且值原样、CONTENT 不进）、`TestShouldForwardToWSLExtraKeys`（导出判据与内部判据一致性）、`TestWriteWSLOpenCodeAgentConfig{WritesAndChmod,FreshDistro,ChmodFailureFailsClosed}`（内容/覆盖语义/chmod 序列/失败清理）——全 PASS。
- **真实 WSL e2e**（`AMAGI_WSL_E2E=1 go test ./internal/launcher/ -run TestManualRealWSL`）：
```
--- PASS: TestManualRealWSLWriteOpenCodeConfig — written to /home/maorun/.config/opencode/amagi-codebox.json (distro Ubuntu), content OK (235 bytes)
--- PASS: TestManualRealWSLWritePiConfig / TestManualRealWSLUserHome / TestManualRealWSLSearchTools（无回归）
```
- 未覆盖项：未在 distro 内跑真实 opencode CLI 加载该文件做端到端请求（distro 内未装 opencode）；`OPENCODE_CONFIG` 附加层语义来自 v1.17.18 二进制字符串反查（证据链见上文），非 live 运行验证。

### T12（hunk：service.go:969-1010）
- `TestWorkDirMapsToDrvFS`：/mnt/d/repo、/mnt/c、/mnt/c/ 命中；/mnt/wsl、/mnt/docker-desktop、/mnt/wsl/fonts、/mnt/usb、/mnt、/mnt/ 不命中；盘符用例与排除用例全维持。
- `TestDrvfsMountPath`：/mnt 形态原样返回、/mnt/wsl 空。
- `TestBuildWSLCommandLineWarnsOnDrvFSWorkDir`（真实 distro）：/mnt/d/repo 恰好一条 WARN 且明细携带挂载路径；/mnt/wsl/fonts 无告警；ext4 无告警——全 PASS。

### T13（hunk：service.go:326-332、1017-1083）
- `TestEnsureWSLUTF8LocaleEnv*` 4 个：注入时同时进 WSLENV；已有 WSLENV 追加不破坏；清单已有 LANG（含 /p 形态）不重复；LANG/LC_ALL/LC_CTYPE 任一存在（含空值、大小写变体）不注入且 WSLENV 不动；入参不被修改——全 PASS。
- 未覆盖项：挂接行为本身（StartResolvedWithRunEvidence→ConPtyEnv 一行接线）未做进程级断言（需真实 ConPTY 会话）；函数级 + 编译期覆盖。

### T6/B3（hunk：app.go:2360-2371、3713、3994；wsl_agent_config.go:162-200；wsl.go:287-322）
- `TestStripWSLHostPathEnv`：PI_/ANTHROPIC_/CLAUDE_/CODEX_/OPENAI_ 前缀、OPENCODE_CONFIG 精确键、extra 自定义键的 Windows 盘符路径值（含正斜杠形态）全剥离且 stripped 键序正确；PI_OFFLINE 等标量、Linux 路径 OPENCODE_CONFIG、不在转发面的 USERPROFILE 保留；extra 列表外的自定义键不剥离——PASS。
- app 层接线（漏斗挂接）：经 go build + 全根包测试零回归佐证（见下）。

## 四、测试结果与预存失败清单（A/B 对照）

Windows 门控 WSL 测试面（真实 distro）全绿：
```
go test ./internal/platform/ -run 'WSL'            → 28 PASS（含新增 2）
go test ./internal/launcher/ -run 'WSL|StripWSLHostPathEnv|OpenCodeAgentConfig' → 13 PASS
go test ./internal/pty/ -run 'WSL|DrvFS|Drvfs|WorkDir|EnsureWSL|LANG'           → 8 PASS
```

本执行通道（WSL 互操作调用 Windows go.exe）存在预存环境失败，已逐一与 HEAD 原版代码 A/B 对照证明与本次改动无关：
- `internal/pty` `TestExactShellAttachReadyThenBootstrapExecutesTargetCLI`：HEAD 原版同样失败（互操作父进程下 ConPTY attach 路径）。
- `internal/launcher` 4 个 perms 测试（`TestWrite{Pi,Omp}AgentConfig{TightPerms,UpgradesLegacyPerms}`）：断言 POSIX 0600/0700 权限位，Windows 文件系统无此语义（报 `perm = 777`）；未触碰相关源码（diff 清单可证）。
- `internal/platform` `TestDarwinFNMDefaultAlias…`：Windows 符号链接需要特权（`A required privilege is not held by the client`）。
- 根包 `go test .`：我的版本失败集 ⊆ HEAD 失败集（29 项逐一相同，另有 1 项 flaky 在我的版本恰好通过），A/B 归一化比对通过。这些测试在仓库 CI 的 macos-latest 全量跑（CLAUDE.md 约定），本机通道限制非回归。

## 五、未覆盖项与风险

1. distro 内真实 opencode CLI 加载验证未做（见 T4）；建议真机冒烟：WSL shell 下启动 opencode 会话，确认模型路由到 amagi provider。
2. `ensureWSLUTF8LocaleEnv` 的进程级接线未断言（函数级已覆盖）。
3. OPENCODE_CONFIG 加入转发面后，用户自有 Windows OPENCODE_CONFIG（Windows 路径值）会被统一剥离防线拦下（T6 覆盖），Linux 路径值不受影响——已在单测钉定。
4. 预存环境失败清单见上节；与本批次无关。

## 六、回滚方式

9 个文件均为未提交工作区改动：`git checkout -- <file>`（单文件粒度）即可回退；`internal/pty/wsl_env_test.go` 为新文件，直接删除。生产文件回滚后无需其它配套（app.go 调用与 launcher/platform 新符号同退）。
