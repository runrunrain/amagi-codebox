# P4-A3 最终报告：MCP Windows 形态检测对称化——前提核验 + A（证据修正）/B（opencode env 补齐）双交付

- 状态：**完成**（Leader 裁决 Q1=A+B、Q2=omp 不立项后执行；终态见 §六验证与 §七回滚）。
- 上游输入：`agent-outputs/wsl-session-friction/p4-evidence.md`（E9）；`agent-outputs/workflow/wsl-session-friction/p3-config-supply-design.md` §四。

## 一、前提失效与根因（本轮首要发现）

任务书前提「claude/codex/pi 的 WSL MCP 落地面无对称检测」经全量核验**不成立**：

- 三 CLI 的全部 WSL MCP 落地面在 p4-evidence 落盘前（2026-10-16 19:27 提交 6b015b8，P3-S4；S2 为 bd93f4c）已统一经 `wslsupply.TransformMCPServer` 做形态检测，规则是 opencode 启动面的**严格超集**（另覆盖：盘符命令可信 shim 位置映射、args 盘符路径已验证映射、args 内嵌盘符、env 路径键与未声明 Windows 形态值）。
- E9「仅 opencode；其余四 CLI 无对称面」根因＝采集盲区：p4-evidence §6 覆盖清单不含 internal/wslsupply。
- 任务书要求的「claude/codex/pi 各一形态命中+放行用例」均已存在且现绿：pi `TestPiMCPConversionFailureKeepsUnitAtomic`（pi_config_test.go:240）/`TestPiMCPServerLifecycle`（:189）；claude `TestClaudeMCPSelectionGate`（claude_config_test.go:92）；codex `TestCodexConfigSelectionAndTOMLFidelity`（codex_config_test.go:83）。
- 负面扫描确认无其它 WSL MCP 落地路径：wslsetup/remoteclient/remote 全域零 MCP 落地；preset schema 无 MCP 字段（preset 携带 MCP 仅 opencode 的 opencode_config 附加 JSON）。

## 二、五 CLI 落地面 × 检测面矩阵（交付后终态）

| CLI | WSL MCP 落地面（真实入口） | 检测实现 | 终态 |
|---|---|---|---|
| opencode | 启动附加层（app_wsl_supply.go `prepareWSLOpenCodeLayer` → `AdaptWSLOpenCodeMCPWithOptions` → `WriteWSLOpenCodeAgentConfig`） | `classifyWSLMCPServer`（internal/launcher/wsl_opencode_mcp.go）：命令形态 + args 盘符 + **env 盘符值（本轮补齐）** | ✓ |
| pi | S2 pi/mcp 键级投影（`registerPiConfigAdapters` → `NewPiMCPAdapter`，pi_shapes.go:177-187） | `TransformMCPServer`（transform.go:257，超集） | ✓ |
| claude | S4 claude/mcp 导入（app_wsl_supply_s4.go:119；claude_adapter.go:85-95） | 同上 | ✓ |
| codex | S4 codex/config mcp_servers 导入（app_wsl_supply_s4.go:121；codex_adapter.go:448-460） | 同上 | ✓ |
| omp | 无 MCP 落地面（launcher 仅写 models.yml；wslsupply 无 omp 作用域） | n/a | 主上裁定不立项（设计 §四「本批不设计」） |

## 三、A 项交付：p4-evidence.md 修正（4 处）

1. 结论索引 E9 行：三态「观察」→「已修正」，主张改「对称化已在 S2/S4 完成（TransformMCPServer 超集）+ opencode 弱版子集已补 env；原『仅 opencode』为采集盲区」，锚点补 transform.go:257，边界改 omp 不立项。
2. §1 P4-E9 小节：追加【P4-A3 修正（Leader 裁决）】段（不成立依据、提交时间线、超集差异枚举、误报根因、B 项补齐说明、报告指引）。
3. §3 切片建议：E9 项改「原对称化切片不成立——S2/S4 已完成，P4-A3 仅补 opencode 启动层 env 检测；omp 裁定不立项」。
4. §6 覆盖范围：补记 wslsupply 盲区教训——「负面对断言必须以对应包的正面检索为据，未读的包不得写『无』」。

## 四、B 项交付：opencode 启动层补 env 盘符值检测

- **改动**：`internal/launcher/wsl_opencode_mcp.go` `classifyWSLMCPServer` 尾部新增 env 检查块（args 检查之后）：`entry["env"]`（map[string]any）中字符串值 TrimSpace 后命中 `isWindowsDrivePath` → 该 server 拒配，原因文案「环境变量 %s 的值为 Windows 盘符路径，需手动适配为 WSL 内路径」。语义边界（按裁决）：
  - 只拒配不映射——可信位置/已验证路径映射是 wslsupply 落地层职责，启动层保持轻量；
  - 键名入诊断（键名非密钥）、值不回显；env 键排序保证首个命中者确定性；
  - 远程条目（带 url）维持既有早退（与 wslsupply 语义一致）；非字符串 env 值跳过；
  - 文件头注释的检查清单同步补「参数/env 值中的盘符路径」。
- **测试**：`internal/launcher/wsl_opencode_mcp_test.go` 新增 `TestAdaptWSLOpenCodeMCPEnvDriveValue`（4 子例）：①env 盘符值命中拒配（键名+盘符+手动适配入诊断、值不回显、server 不落附加层）②多命中键字典序确定性（A_DIR 先于 Z_DIR）③非盘符 env（标量/Linux 路径/URL）放行且原样保留（回归）④`PreviewWSLOpenCodeMCP` 预览面一致呈现 NeedsAdapt。
- 既有 opencode MCP 测试（S1/S4 批）零改动零回归（§六）。

## 五、omp（Q2）

不立项（主上裁定：B7 同族的声明性排除延伸）。本轮零动作；矩阵与证据文档已如实标注。

## 六、验证证据（全部自跑；环境 `/mnt/c/Program Files/Go/bin/go.exe`，GOFLAGS=-mod=vendor，GOCACHE 经 WSLENV 转发）

| 项 | 命令/操作 | 结果 |
|---|---|---|
| launcher MCP 面 | `go test ./internal/launcher -run "MCP" -count=1 -v` | **ok**，含新 TestAdaptWSLOpenCodeMCPEnvDriveValue 4 子例全 PASS，既有 8 个 MCP 测试全 PASS |
| wslsupply 全包 | `go test ./internal/wslsupply -count=1` | **ok**（2.065s） |
| launcher 全包 | `go test ./internal/launcher -count=1` | 4 FAIL——**环境性预存**：TestWrite{Omp,Pi}AgentConfig{TightPerms,UpgradesLegacyPerms} 断言 POSIX 0700/0600，Windows NTFS 上 Go os.Chmod 无法表达（CI 设计：windows-latest 仅编译检查 `-run '^$'`，全量只在 macos-latest）；与本改动无因果（测试文件在 HEAD，失败机制为平台差异） |
| 根回归 | `go test . -run "WSL|MCP|OpenCode|Claude|Codex" -count=1` | 3 FAIL——**环境性预存**：R9_002/R6_001 为 `exec: "sleep": executable file not found in %PATH%`（POSIX 命令缺失）；ConfigMutationCAS 为 `config sync-directory: Access is denied.`（Windows 目录权限）；均不涉 MCP 检测面，与本改动无因果 |
| vet | `go vet ./internal/launcher/ ./internal/wslsupply/` | 零输出 |
| build | `go build ./...` | exit 0（含兄弟任务并发中间态） |
| gofmt | `gofmt -l`（只读）两个改动文件 | 零输出（漂移已用单文件定向 `gofmt -w` 修正后复检） |

注：树上有兄弟任务（P4-A1 DrvFS advisory / P4-A2 path allow）未提交改动并发落盘；上述「ok」以本会话快照为准，FAIL 项已逐条归因为环境/预存而非兄弟语义改动或本改动。

## 七、共享树事故留痕（Leader 已知悉）

- 本会话曾两次误用写形态 `go fmt`（整包）：state.go 的 1 字符对齐漂移已精确还原至 HEAD 字节一致（wslsupply 包 diff 为零）；wsl_agent_config.go 的纯格式化字节与兄弟 P4-A2 未提交语义改动混合、不可分离（回滚=销毁兄弟工作），保留（gofmt-clean、语义零变化），Leader 在 A2 审核时统一核。全程未 stash/checkout/restore/reset。

## 八、改动文件与回滚

| 文件 | 改动 | 回滚 |
|---|---|---|
| internal/launcher/wsl_opencode_mcp.go | classifyWSLMCPServer 补 env 盘符值检测 + 头注释同步（+21 行） | 删除新增 env 检查块与注释两行 |
| internal/launcher/wsl_opencode_mcp_test.go | 新增 TestAdaptWSLOpenCodeMCPEnvDriveValue（+72 行） | 删除该测试函数 |
| agent-outputs/wsl-session-friction/p4-evidence.md | E9 修正 4 处（索引行/§1/§3/§6） | git 未跟踪文件，按需还原对应段落 |
| agent-outputs/workflow/wsl-session-friction/p4a3-report.md | 本报告 | 删除文件 |

回滚均无级联影响（无公共契约变化、无消费者变更）。

## 九、未覆盖项/风险

- opencode 启动层仍不做盘符命令的可信位置映射（裁决明确的职责边界，非缺口）。
- env 检测只覆盖「独立盘符路径值」；内嵌盘符形态（如 `--config=C:\x`）与 %VAR% 变量路径形态在启动层不检（wslsupply 落地层对导入面全检；preset 携带该形态的场景建议后续观察再议，本轮按裁决最小补齐）。
- 「ok」结论受共享树并发快照限制；最终结算以隔离快照复跑为准。
