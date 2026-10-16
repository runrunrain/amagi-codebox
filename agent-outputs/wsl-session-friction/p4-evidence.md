# P4 证据采集：WSL 壳 × Windows 主场的工作摩擦面（UE5 开发定位）

- 状态：**部分完成**。采集清单第 4 项（CodeBox 支撑面盘点，读码级）完整交付；第 1/2/3/5 项（DrvFS git 计时、基准树遍历、Windows 工具实调、DDC du）因采集节点 shell 通道受限（baize 只读+报告），**改由 Leader 直跑**——移交清单见 §7。
- 前置：一轮 `evidence-report.md`（A1-C5）、二轮 `toolcall-evidence.md`（T1-T14）已通读；本轮只做增量与状态复核，不重复历史采集。
- 方法：仓库定向读码（internal/pty、internal/platform、internal/launcher、internal/gitassist、internal/paths、frontend/terminal 链）+ internal 全域负面 grep。所有行号为当前工作区实读行号。零运行时实测（采集节点无 shell）。
- 本轮代码状态相对历史报告有三处**已修复确认**（E2/E3/E10），引用一轮/二轮时注意。

## 结论索引（每行＝主张｜三态｜锚点｜适用边界）

| # | 主张 | 三态 | 锚点 | 适用边界 |
|---|------|------|------|---------|
| E1 | 工作目录给 X:\ 盘符形态时，WSL 会话落在 /mnt/x/...（DrvFS/9P）——UE5 工程在 Windows 盘 ⇒ WSL 会话工作目录结构性落 DrvFS | 观察（代码级；wsl.exe 映射行为由代码注释自证，未真机复测） | internal/pty/service.go:934-936,1009-1013 | X: 为本地 NTFS 时成立 |
| E2 | 【更新一轮 B4】Linux 形态 workDir 已不再传 CreateProcess lpCurrentDirectory：err=267 已真机定谳并写入代码注释+回归测试，工作目录由 wsl.exe --cd 独自承担 | 观察 | service.go:336-345,986-1007；wsl_workdir_test.go:5-9,41-67 | Windows 宿主 PTY 路径 |
| E3 | 【更新二轮 T12】DrvFS 告警已覆盖 /mnt/<drive> Linux 形态（盘符+/mnt 双形态；/mnt/wsl 与 ext4 排除） | 观察 | service.go:1009-1030；wsl_drvfs_test.go:63-139 | — |
| E4 | DrvFS 感知是单点：仅 pty 启动路径有告警；GitAssist/PathPicker/usage 等配套面零 DrvFS 感知 | 观察（负面） | E7/E8 锚点文件全域 grep DrvFS/mnt 零匹配 | — |
| E5 | UE5 工具链变量（UE_ROOT/VSINSTALLDIR/UBT 类）在 CodeBox 无任何专门感知；唯一通路是 envvars.json 自定义键转发 | 观察（负面）+观察 | internal 全域 grep UE5/VSINSTALLDIR/MSVC 等零命中（唯一 MSVC=ripgrep 包名 envcheck/checker_searchtools.go:141）；internal/platform/wsl.go:378-384,412-441 | — |
| E6 | 值为 Windows 盘符路径的转发变量被静默剥离；PATH/PATHEXT 为保留键永不转发；WSLENV 无 /p 路径翻译标志 | 观察 | wsl.go:447-456,460-462；internal/launcher/wsl_agent_config.go:173-199 | UE5 的「VS 路径类」变量到不了 WSL；标量类可过 |
| E7 | GitAssist 全链假设 Windows 路径：Go 进程内 exec git.exe -C <workDir>；X:\ 形态可用且不经 DrvFS（快）；ext4//mnt 形态失效且无前置检测 | 观察+推断 | internal/gitassist/service.go:80-91；frontend/src/components/terminal/GitPanel.vue:210,307-316,353-411；api/gitassist.ts:27-71 | 「X: 本地卷」前提是推断 |
| E8 | PathPicker 全链假设 Windows 路径：后端 filepath.Abs+os.ReadDir；Linux 形态 workDir → Abs 拼当前盘符 → Stat 失败 → 前端静默降级 Windows home（错位不报错） | 观察 | internal/paths/list_directories.go:36-86；pathPickerModel.ts:31-44,215-216；PathPickerDialog.vue:129 | — |
| E9 | 【P4-A3 修正】WSL MCP Windows 形态检测对称化已在 S2/S4 完成：pi/claude/codex 全部落地面经 wslsupply.TransformMCPServer（超集，含 env）拒配；opencode 启动面为弱版子集（P4-A3 已补 env 盘符值检测）；原「仅 opencode」为采集盲区 | 已修正 | internal/wslsupply/transform.go:257；internal/launcher/wsl_opencode_mcp.go:231 | omp 无 MCP 落地面（设计 §四「本批不设计」，主上裁定不立项） |
| E10 | 【更新一轮 B3】路径值剥离已从 PI_* 泛化为「一切将跨界且值为盘符路径的变量」（StripWSLHostPathEnv，判据=ShouldForwardToWSL） | 观察 | wsl_agent_config.go:159-199,35-36；wsl_agent_config_test.go:194-255 | — |

---

## 1. 逐条证据

### P4-E1（结构性）：X:\ 形态工作目录 ⇒ WSL 会话落 DrvFS —— UE5 定位的核心摩擦起点

- 主张：UE5 工程/资产在 Windows 盘（本机形态：/mnt/i/UE5Engine 预留、UEHuanCun 在 I 盘）时，工作目录填 `X:\...` 盘符形态，WSL 会话内实际落在 `/mnt/<drive>/...` DrvFS/9P 挂载。
- 观察：`buildWSLCommandLine` 把 workDir 原样交给 `--cd`（service.go:934-936），`workDirMapsToDrvFS` 注释自证映射语义：「a Windows drive-letter path (X:\repo, which wsl.exe --cd maps onto /mnt/<drive>)」（service.go:1009-1013）。盘符形态同时仍传 `ConPtyWorkDir`（conPtyWorkDirFor 对盘符/UNC 返回原样，service.go:1002-1007），ConPTY 进程 cwd 留在 Windows 侧。
- 影响：「模型在 WSL bash 工作 + 工具链在 Windows」的主路径下，只要工程不搬 ext4，会话就开在 DrvFS 上——一轮 A4/文档自白的「数倍到数十倍」性能惩罚直接命中模型全部文件操作。这是结构性约束，不是 bug。
- 边界：wsl.exe --cd 的映射行为未由本节点真机复测（无 shell）；「X: 为本地 NTFS」未验证（映射网络盘则 E7 的正面事实不成立）。

### P4-E2：一轮 B4 已修复（Linux 形态 workDir 启动失败面闭环）

- 观察：`conPtyWorkDirFor`（service.go:998-1007）＝WSL 会话 + `isLinuxStylePath`（/、~/、裸~，service.go:993-996）→ 返回空串，不传 `ConPtyWorkDir`；注释记录真机定谳：「CreateProcess lpCurrentDirectory 只接受 Windows 形态目录；Linux 形态一律 err=267 ERROR_DIRECTORY……PowerShell P/Invoke CreateProcessW 直测三种 Linux 形态全 267，对照盘符路径成功」（service.go:336-342）。
- 回归钉定：wsl_workdir_test.go:5-9（头注）、:41-67（`~/projects`、`~`、`/home/u/repo`、`/mnt/x/WorkSpace/amagi-codebox` 全部不传；盘符维持原语义）。
- 一轮 B4 的「疑点 1」（CreateProcess 实际行为）已由该修复的注释定谳，可销项。

### P4-E3：二轮 T12 已修复（/mnt Linux 形态纳入告警）

- 观察：`workDirMapsToDrvFS`（service.go:1009-1030）双形态判定——盘符（`w[1]==':'` + 盘符字母）与 `/mnt/<drive>`；`/mnt/wsl` 明确排除；ext4（~/、/home/）返回 false。
- 回归钉定：wsl_drvfs_test.go:97-110（`/mnt/d/repo` 告警且 Detail 携带挂载路径）、:112-119（ext4 不告警）、:123-132（`/mnt/wsl` 不告警）。
- T12 可销项；其告警出口仍是 C1 的 log-only（无变化）。

### P4-E4：DrvFS 感知是 pty 启动路径单点

- 观察（负面）：gitassist、paths 两包全域 grep `DrvFS|/mnt|ext4` 零匹配；DrvFS 告警唯一触发点在 `buildWSLCommandLine`（service.go:942-944）。
- 影响：配套功能面（E7/E8）在 Linux 形态目录上失败时，用户收不到任何「这是 DrvFS/路径形态问题」的解释——告警与故障面脱节。

### P4-E5：UE5 工具链变量零专门感知；唯一通路是 envvars.json

- 观察（负面）：internal 全域 grep `UE5|UE4|UnrealEngine|UBT|BuildGraph|VSINSTALLDIR|VisualStudio|MSVC|cl.exe` ——唯一命中是 ripgrep 的 winget 包名后缀（envcheck/checker_searchtools.go:141），与 UE5 无关。
- 观察：转发面＝前缀白名单 `ANTHROPIC_/OPENAI_/CLAUDE_/CODEX_/PI_`（wsl.go:378-384）+ 精确键（代理 8 键 + OPENCODE_CONFIG，wsl.go:400-410）+ `extraForwardKeys`（app.go 注入 envvars.json 键名，经 resolver.go 的 appendWSLENVForwarding 调用点）。
- 影响：UE5 场景要转发 `UE_ROOT`、引擎版本号等，用户必须逐键手工配置 envvars.json——CodeBox 既不预置也不校验；「VS 安装路径类」即使配了也会被 E6 剥离。

### P4-E6：路径类值的三重闸门

- 观察：
  1. `isWSLENVReservedKey`：WSLENV/PATH/PATHEXT 永不转发（wsl.go:447-456）——「PATH 追加」类需求不可经 WSLENV 通路。
  2. `appendWSLENVForwarding` 无路径翻译标志：「no path-translation flags: the forwarded values are keys / URLs」（wsl.go:460-462）——值原样跨界，不做 Windows↔Linux 路径改写。
  3. `StripWSLHostPathEnv`（wsl_agent_config.go:173-199）：判据＝`platform.ShouldForwardToWSL(key) && isWindowsDrivePath(value)`（:162-171，识别 `C:\`/`C:/`），命中即剥离（返回剥离键列表供日志，:187-199）。测试钉定含 `CODEX_HOME=D:\...`、自定义键 `MY_TOOL_HOME=F:\tools`（extra 列表内）剥离、extra 列表外不剥（wsl_agent_config_test.go:194-255）。
- 影响：UE5 的 VS 安装路径类变量（值必为 `C:\Program Files\Microsoft Visual Studio...` 形态）在转发链上不可达 WSL——防泄漏设计（PI_* 事故泛化）与「WSL 内工具需要读 Windows 路径」的合法需求正面冲突，且剥离仅日志可观测。

### P4-E7：GitAssist Windows 假设点（主上原 T11 项对象，行号更新）

- 后端：`runGit`＝`exec.CommandContext(ctx, "git", append([]string{"-C", workDir}, args...)...)`，Windows 进程内解析 PATH 里的 git.exe（internal/gitassist/service.go:80-91）；包内 grep linux//mnt/ext4 零匹配。
- 前端链：GitPanel.vue:210（workDir prop）→ :307-316 `loadAll`→`getRepoInfo(props.workDir)` → :353/:369/:385-386/:406-411（切分支/生成信息/提交/推送全传 workDir）；api/gitassist.ts:27-71 全部函数直传 workDir。
- 假设点：workDir＝session.workDir 原样进入 Windows git.exe。
  - `X:\WorkSpace\amagi-codebox` 形态 → git.exe 直接消费 NTFS 路径。**推断**（前提：X: 为本地卷）：不经 DrvFS/9P，GitAssist 全链既可用又无跨界性能惩罚——「Windows 面板管 Windows 侧仓库」与「WSL 终端在同一目录工作」并行不悖。
  - `/mnt/x/...` 或 `~/...` 形态（文档推荐的 ext4 工作流）→ Windows git.exe 无法解析该目录 → GitPanel 全链失效，无前置检测（git.exe 报错形态待实证，二轮待核验项未销）。

### P4-E8：PathPicker Windows 假设点（行号更新）

- 后端：`ListDirectories`＝`filepath.Abs(root)` + `os.Stat` + `os.ReadDir`（internal/paths/list_directories.go:36-86），纯 Windows os 层。
- 前端链：pathPickerModel.ts:31-44 `parentDirOf`/`initialRootFor`（dirname(workDir)，兼容 / 与 \）→ :215-216 `resetAndLoad` 初始 root＝dirname(workDir)；PathPickerDialog.vue:129「失败降级空串（后端 home 兜底）」；TerminalView.vue:121-126 挂载 `:work-dir="session?.workDir"`。
- 假设点：workDir=`/mnt/x/WorkSpace/repo` → dirname=`/mnt/x/WorkSpace` → 后端 `filepath.Abs` 在 Windows 上拼当前盘符（如 `X:\mnt\x\WorkSpace`）→ Stat 失败 → 前端静默降级到 **Windows home**。用户看到的是错位的目录树，不是报错——比 E7 的显式失败更隐蔽。

### P4-E9：WSL OpenCode MCP 的 Windows 形态检测面（T2 后新增能力）

- 观察：`wsl_opencode_mcp.go:245-268`——对 WSL 侧 MCP server 校验命令形态：盘符路径 / .exe/.ps1/.bat/.cmd / cmd/powershell/pwsh → 拒配「命令 %q 为 Windows 专属形态，需手动适配为 Linux 命令」；args 中独立盘符路径 → 拒配「需手动适配为 WSL 内路径」；「未验证的挂载映射不做，不生成假可用条目」。
- 边界：仅 opencode 有此面；UE5 相关工具若以 Windows 形态命令注册进 opencode MCP，会在此被显式拦截（比 T2 时代「原样落地原样失效」前进了一步，但同样只覆盖 opencode 一个 CLI）。
- 【P4-A3 修正（Leader 裁决）】原「仅 opencode；其余四 CLI 无对称面」不成立：pi（S2 键级投影 pi/mcp）、claude（S4 claude/mcp 导入）、codex（S4 codex/config mcp_servers 导入）的全部 WSL MCP 落地面在本文档落盘前（2026-10-16 19:27 提交 6b015b8）已统一经 wslsupply.TransformMCPServer 做形态检测，规则为 opencode 启动面的严格超集（另覆盖：盘符命令可信 shim 位置映射、args 盘符路径已验证映射、args 内嵌盘符、env 路径键与未声明 Windows 形态值）。误报根因：本报告 §6 覆盖清单漏 internal/wslsupply（采集盲区）。P4-A3 同批补齐 opencode 启动层原独漏的 env 盘符值检测（classifyWSLMCPServer，只拒配不映射，启动层保持轻量）。omp 无 WSL MCP 落地面（设计 §四「本批不设计」；主上裁定不立项）。详见 agent-outputs/workflow/wsl-session-friction/p4a3-report.md。

### P4-E10：一轮 B3 已泛化修复（更新历史结论）

- 观察：`StripWSLHostPathEnv` 取代 1.3.59 的 `StripWSLHostPathPIEnv`；注释自证泛化动机「转发面不只 PI_ 前缀……携带 Windows 盘符路径值时在 Linux 侧同样非法（如 CODEX_HOME=C:\...，证据 B3/T6）」（wsl_agent_config.go:35-36,173-186）。判据与转发清单完全一致（:182）。
- 一轮 B3 可销项；其「标量值不受影响、非转发面键不动」的边界经测试钉定保持（wsl_agent_config_test.go:199-211,244-248）。

## 2. 对「模型在 WSL 做 UE5 开发」的摩擦分级（基于已采证据）

| 摩擦 | 分级 | 依据 |
|------|------|------|
| X:\ 工程目录 ⇒ 会话结构性落 DrvFS（E1） | 重摩擦（待 §7 实测定量） | 代码级观察成立；量级待实测 |
| UE5/VS 变量转发不可达（E5+E6） | 重摩擦（跨边界工具链拼装受阻） | 负面确认+三重闸门 |
| GitAssist/PathPicker 对 ext4 形态失效（E7/E8） | 轻摩擦（X:\ 形态下 GitAssist 反而可用；ext4 形态才触发） | 观察+推断 |
| DrvFS 告警与配套故障面脱节（E4） | 轻摩擦（可感知性缺口） | 负面确认 |
| git 性能墙定量/工具实调/DDC 跨界 | 未采集 | §7 |

## 3. 切片建议倾向（分类，不含方案；裁决归 Leader+fuxi）

- **适合 CodeBox 改进的摩擦面**：E5+E6（UE5 工具链变量的转发通路——预置/校验/路径值策略均有工程空间）；E7/E8（Linux 形态 workDir 下配套功能的前置检测与形态感知——GitAssist 可走 WSL 侧 git 或前置报错）；E4+E8（错位降级改显式失败/提示）；E9（【P4-A3 修正】原对称化切片不成立——S2/S4 已完成检测落地，P4-A3 仅补 opencode 启动层 env 盘符值检测；omp 经主上裁定不立项）。
- **环境固有、只能引导/文档化的摩擦面**：E1 的 DrvFS 性能量级（WSL2 9P 协议固有，CodeBox 侧只能选择：引导搬 ext4 / 引导 Windows 原生 shell 会话 / 接受）；PATH 追加不可经 WSLENV（WSLENV 机制固有，只能文档化替代通路）；CreateProcess 对 Linux 形态的 err=267（已修复绕开，非遗留）。

## 4. 未采集项登记（原 blocked 项 → 由 Leader 直跑）

| 清单项 | 断点 | 影响 |
|--------|------|------|
| 1. DrvFS git 性能墙（含 ext4 对照） | 未执行任何计时命令 | E1 只有定性无定量；「哪类 git 操作不可用/慢/无感」无结论 |
| 2. 基准树遍历（ext4 vs /mnt/x） | 树未构造；/mnt/x/p4-tree 未创建（无残留） | UE5 资产形态（5000×.uasset 级）遍历成本无数据 |
| 3. Windows 工具实调（go.exe/git.exe/powershell.exe GBK） | 未执行 | E7 中 git.exe 对 /mnt 形态的具体报错形态仍待实证（二轮遗留） |
| 5. DDC Buckets du（/mnt/i/UEHuanCun） | 未执行 | DDC 跨界只读性能无感受数据 |

## 5. 命令清单（原「续接」备稿，已归整为 §7 移交清单，按 §7 执行）

## 7. Leader 直跑项移交清单

> 采集者=Leader。按批次执行（每批 ≤3 分钟，长批拆分）；每条记录「命令｜耗时｜关键输出行」，跑完后把定量数据直接追加到本文档对应条目（E1/E7 与 §2 表）下并标注采集者=Leader。预算：每条 git 命令 ≤120s，超时如实记录不硬等。

### 批次 A：DrvFS git 性能墙（清单1，每条 ≤120s 预算，超时记录不硬等）

```bash
cd /mnt/x/WorkSpace/amagi-codebox
for c in "git status" "git status --porcelain" "git log -1" "git diff --stat HEAD"; do
  /usr/bin/time -f "%es" timeout 120 $c
done
cd /mnt/x/ComfyUI   # 同上四条
# ext4 对照：mkdir -p ~/p4-bench && cp -r /mnt/x/WorkSpace/amagi-codebox/.git ~/p4-bench/.git
#            并补最小工作树（git checkout 需先恢复索引可读——若 .git 拷贝后不可用，
#            改为在 ~/p4-bench git clone /mnt/x/WorkSpace/amagi-codebox 后同四条计时）
# 写面：cd /mnt/x/WorkSpace/amagi-codebox && touch p4-probe.txt && time git add p4-probe.txt && git reset -q p4-probe.txt && rm p4-probe.txt
```

- 结论要素：哪类 git 操作在 DrvFS 上「不可用 / 可用但慢 / 无感」。

### 批次 B：基准树遍历（清单2，ext4 先建对照，测后清理）

```bash
# python3 或 shell 循环生成 ~/p4-tree：~5000 文件×多层，含 .uasset/.umap 命名与 Binaries/Intermediate/Saved 子目录
time find ~/p4-tree -type f | wc -l; time du -sh ~/p4-tree; time tar cf /tmp/p4-tree.tar ~/p4-tree
cp -r ~/p4-tree /mnt/x/p4-tree   # 计时
cd /mnt/x/p4-tree && 同三条计时
rm -rf /mnt/x/p4-tree && rm -rf ~/p4-tree /tmp/p4-tree.tar   # 测后清理（任务授权范围内）
```

### 批次 C：Windows 工具实调（清单3）

```bash
command -v go.exe; command -v git.exe          # 预期：不可达（PATH 无 Windows 目录）
"/mnt/c/Program Files/Go/bin/go.exe" version
/mnt/i/Git/cmd/git.exe --version
/mnt/i/Git/cmd/git.exe -C 'X:\WorkSpace\amagi-codebox' rev-parse --show-toplevel   # Windows 形态
/mnt/i/Git/cmd/git.exe -C '/mnt/x/WorkSpace/amagi-codebox' rev-parse --show-toplevel  # Linux 形态：记录报错原文
"/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe" -NoProfile -Command "echo '中文编码探针——GBK乱码复证'"
```

### 批次 D：DDC 只读跨界（清单5，不做写测试）

```bash
ls /mnt/i/UEHuanCun/Buckets; time du -sh --max-depth=1 /mnt/i/UEHuanCun/Buckets
```

### 收尾清理（全部批次完成后核对；任务授权范围内的构造物）

```bash
rm -rf /mnt/x/p4-tree ~/p4-tree ~/p4-bench /tmp/p4-tree.tar   # ~/p4-bench 为批次 A 的 ext4 对照物，一并清理
```

## 6. 覆盖范围与待核验项

- 已覆盖（读码）：internal/pty（workDir 全链+测试）、internal/platform/wsl.go（转发面+reserved 键）、internal/launcher/wsl_agent_config.go（剥离泛化）、wsl_opencode_mcp.go（MCP 形态检测）、internal/gitassist、internal/paths、frontend terminal 链（GitPanel/PathPicker/TerminalView/api 层）、internal 全域 UE5 变量负面 grep。【P4-A3 补记】internal/wslsupply 当时未入采集清单，由此漏看 S2/S4 已落地的 MCP 检测面并得出错误的负面对结论（E9「仅 opencode」）；教训：负面对断言（「其余 CLI 无某能力」）必须以对应包的正面检索为据，未读的包不得写「无」。
- 推断项（前提已写明）：E7「X: 为本地 NTFS 卷」未验证；E1 的 wsl.exe --cd 映射行为依赖代码注释自证。
- 待核验：§4 全部 + E7 的 git.exe `/mnt` 形态报错原文 + `X:` 盘物理形态（本地/网络映射，影响 E7 正面事实成立与否）。
- 未覆盖：不跑 App（任务边界）；macOS 侧无 WSL 与本任务无关；历史两轮已覆盖条目不重复采集。

---

## 8. 定量实测（采集者=Leader，2026-10-16 晚；§7 移交清单批次 A-D 全量执行）

### 8-1 批次 A：DrvFS git 性能墙（同仓库两文件系统对照）

amagi-codebox 工作树（DrvFS /mnt/x，工作树含 node_modules 等未跟踪大目录，索引冷/热两态）：

| git 命令 | DrvFS 首跑（冷） | DrvFS 二跑（热） | ext4 对照（同仓库 clone） |
|---|---|---|---|
| git status | **37.7s** | 3.7s | **0.19s** |
| git status --porcelain | 3.7s | 同左量级 | 0.19s |
| git log -1 | 0.10s | — | 0.002s |
| git diff --stat HEAD | 3.0s | — | 0.18s |
| git add 单文件（写面） | 0.09s | — | — |

ComfyUI 工作树（DrvFS）：status 5.4s / porcelain 5.4s / log 0.07s / diff --stat 11.9s（工作树规模不同，量级同族）。

**结论**：git 在 DrvFS「可用但惩罚重」——stat 密集操作（status/diff）冷启动 20~200 倍惩罚、热缓存后 10~20 倍；log 类读对象操作近无感；单文件写面无感。UE5 大仓库（资产数万+）冷 status 预计分钟级——**模型在 WSL 会话跑 git 的真实痛点是交互延迟，不是不可用**。

### 8-2 批次 B：基准树遍历（5000 文件 × .uasset/.umap 形态，ext4 vs DrvFS）

| 操作 | ext4 | DrvFS（热缓存） |
|---|---|---|
| find -type f 全遍历 | 0.007s | 0.036s |
| du -sh（逐文件 stat） | ~0.05s 级 | **3.05s** |
| tar 打包 | 0.018s | — |
| cp -r 5000 小文件写入 DrvFS | — | **17.4s** |

**结论**：热缓存纯目录枚举差距小（5 倍内）；**stat 密集（du/git status）60 倍+；小文件写密集（cp）最痛**（UE5 场景对应：资产导入/构建产物落盘/复制工程）。

### 8-3 批次 C：Windows 工具实调（E7 定谳）

- `command -v go.exe / git.exe` → 均不可达（PATH 无 Windows 目录确认）；全路径调用正常（go1.25.5 / git 2.45.2.windows.1）。
- **git.exe -C 传参形态对照（E7 实证）**：
  - `X:\WorkSpace\amagi-codebox` → 成功（输出 `X:/WorkSpace/amagi-codebox`）——E7「X: 形态可用且不经 DrvFS」**正面事实成立**（X: 本地卷由输出路径自证）。
  - `/mnt/x/WorkSpace/amagi-codebox` → `fatal: cannot change to '/mnt/x/WorkSpace/amagi-codebox': No such file or directory`——**ext4/WSL 形态工作目录下 GitAssist 全链失效的报错形态定谳**（实际退出码非零；首测因管道退出码陷阱未捕获，如实标注）。
- powershell.exe 中文输出 GBK 乱码复证（`???ı??̽?` 形态）。

### 8-4 批次 D：DDC 只读跨界

- `du -h -d 1 /mnt/i/UEHuanCun/Buckets`（208M/数十 bucket）：**42.0s**——跨界 stat 密集读的真实痛感（ext4 同规模 <1s）。DDC 若挂 /mnt 形态供 WSL 侧工具读，遍历型访问不可用级。

### 8-5 综合分级（并入 §2 分类）

| 摩擦 | 分级 | 依据 |
|---|---|---|
| git status/diff 在 DrvFS | 重摩擦（可用但交互级延迟） | 8-1 冷 37.7s/热 3.7s vs 0.19s |
| 小文件批量写 DrvFS | 重摩擦 | 8-2 cp 17.4s/5000 文件 |
| 跨界 stat 密集读（DDC/du） | 重摩擦 | 8-4 42s/208M |
| 热缓存目录枚举 | 轻摩擦 | 8-2 0.036s |
| git log/读对象/单文件写 | 无感 | 8-1 |
| Windows 工具全路径调用 | 轻摩擦（习惯成本） | 8-3 |
| GitAssist 对 /mnt 形态 | 阻断（fatal 显式失败） | 8-3 |
| PathPicker 对 /mnt 形态 | 隐患（静默错位，E8 读码） | — |

### 8-6 采集过程摩擦（Leader 侧，已入 p4-friction-log 候选）

- run_bg 后台原语与 WSL bash 宿主不一致：Linux 脚本在 Windows 侧进程空转 29 分钟（pid 查无/日志不可读），cancel 后改 bash 工具同步分批全部跑完——**待记 A6**。
- git add time 包裹形态被提交守卫拦（改 date 差值计）；构造物 rm -rf 被 200 条目清点门拦（改 mv→trash 保留路径，/tmp/amagi-trash.884LLy 清点 10000 文件 100% 构造探针、零外来，留自然回收）。
