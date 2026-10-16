# QReview：P4-A1+A3 合并阶段 diff（quick，基线 HEAD=e1a6b13，工作区未提交）

- 审核者：谛听（独立检验）
- 范围：A1（paths/gitassist/platform advisory/形态提升/前端 pathPicker·GitPanel/wailsjs）+ A3（wsl_opencode_mcp env 检测 + p4-evidence.md E9 修正）
- 边界排除：wsl_agent_config*.go、resolver.go/wsl.go EnvPathFlagKeys（A2 面）——未审，仅只读交叉引用 isWindowsDrivePath（A3 消费的共享 helper）
- 工具面限制：本轮无 bash → 无法执行 git diff / go test / vitest / vue-tsc。证据基座 = 工作区源码+测试静态复核 + A1/A1 报告自跑证据（agent-outputs/workflow/wsl-session-friction/p4a1-report.md §3）+ 历史报告引证 HEAD 语义（p4-evidence.md:42 引证 HEAD 判据=盘符 `w[1]==':'`+盘符字母+/mnt 单盘符，与现 platform 实现逐条一致）。测试运行态结果未独立复跑，列为盲区。

## 逐关注点结论

| # | 关注点 | 结论 | 关键证据 |
|---|---|---|---|
| 1 | PathPicker 不静默降级 | 通过。后端五形态（/mnt/c、~/、~、/、/home）全拒为 `*LinuxStylePathError`（list_directories.go:66-76；list_directories_wsl_windows_test.go:19-38 errors.As+marker 钉定）；前端 loadRoot catch 先识别 marker→return，形态分支不触及 fallback 判定，两分支互斥（pathPickerModel.ts loadRoot）；retry=`listing?.root \|\| linuxFormRoot \|\| ''`，形态态下重发原 Linux root，单次用户触发无递归兜底→无死循环（测试 :334-343 钉定 calls=[原root,原root]）；裸 `~` workDir→parentDirOf=''→home 兜底边界保留成立。marker 经 Wails 错误透传链（callApi 原样 rethrow→errText）保留全文 | list_directories.go:66-76；pathPickerModel.ts:241-266；pathPickerModel.test.ts:295-377 |
| 2 | GitAssist 前置拦截覆盖 | 通过。7 公共方法（RepoInfo/ListBranches/SwitchBranch/SummarizeDiff/CommitAll/CommitStaged/Push）入口各拦 + 3 exec 构造点（runGit:115 / commitViaStdin:492 / Push push 闭包:544）兜底；包内 `exec.CommandContext` 全量 grep 仅此 3 处→无旁路。RepoInfo 拦截返回 error 不吞成 IsGitRepo=false（service.go:145-148）；前端新空态 `!repo && actionError`（GitPanel.vue:26-29）排在「非 git 仓库」空态之前，loadAll catch 置 repo=null+actionError→渲染条件成立；操作类失败 repo≠null→走既有 banner 不误入空态。SummarizeDiff 测试用 New(nil,nil,nil) 恰证明 validate 先于 settings 访问（否则 nil panic） | service.go:52-65,109-116,145-148,490-496,543-548；service_wsl_windows_test.go:17-59（7×5 全拦+无 fatal 泄漏断言） |
| 3 | advisory 面并发/去重/只读 | 通过。单 mutex 全覆盖写读（drvfs_advisory.go:45-70）；(Source,WorkDir) 原位去重；溢出 `append(nil, s[overflow:])` 重分配丢弃最旧，50 上限成立；查询返回深拷贝切片。pty 原有 log.Warn 语义不变：同条件同文案，Record 为前置追加且 log==nil 也记录（pty/service.go:945-956 与注释声明一致）。GetDrvFSAdvisories 只读、nil→`[]` 归一（app_drvfs_advisory.go:23-31）。JSON 载荷形态钉定（drvfs_advisory_test.go:83-95）。同包测试无 t.Parallel→全局状态无竞争 | drvfs_advisory.go；drvfs_advisory_test.go；app_drvfs_advisory.go |
| 4 | 形态判定提升等价性 | 通过（静态比对成立）。IsLinuxStylePath（/、~/、裸~、TrimSpace、~user/ 排除）与 WorkDirMapsToDrvFS（盘符+isDriveLetter、/mnt 单盘符、/mnt/wsl 等多字母排除、TrimSpace）与 HEAD 语义逐条一致；wsl_drvfs_test.go/wsl_workdir_test.go 用例集覆盖 platform 实现全部分支（含 isLinuxDrvFSMount 的前缀/空段/非字母/单字符/斜杠分支）→「原样全过=真等价」成立。macOS：wsl_form_other.go 恒 false/""；pty 侧消费点（conPtyWorkDirFor/告警）均在 BootstrapWSL 分支，非 Windows 不可达→行为不变声明成立；windows-tagged 测试与 CI 布局为既有形态（windows-latest 只编译、macos 不编译 windows-tag 文件，本批未改变该格局） | wsl_form_windows.go/wsl_form_other.go；wsl_drvfs_test.go；wsl_workdir_test.go；p4-evidence.md:42 |
| 5 | A3 env 检测 | 通过。classifyWSLMCPServer env 段只拒配不映射（无任何值改写）；reason 仅含键名（fmt.Sprintf %s=k），值不回显——测试断言 report 不含 `D:\\tools\\mcp`；键序 sort.Strings→首错确定性（Z_DIR/A_DIR 测试钉 A_DIR 优先）；远程早退 hasMCPRemoteURL 在函数首位未被触碰。弱版子集边界（C:foo、%VAR% 形态不拦）与声明一致（严格超集在 wslsupply.TransformMCPServer，E9 修正锚点 transform.go:257 已核实真实存在且含 env 检测） | wsl_opencode_mcp.go:226-239；wsl_opencode_mcp_test.go:178-241 |
| 6 | wailsjs | 通过。App.js GetDrvFSAdvisories 无参直调形态与同文件无参方法（CheckPiPluginWSLStatus）一致，无多余 arg 占位；App.d.ts `Promise<any>`+「wails generate 再生归一」注释（P3-S5 先例）；models.ts 无需动 | App.js:891-893；App.d.ts:462-465 |

E9 证据修正核对：结论索引三态改「已修正」、§1 逐条补记（wslsupply 锚点真实、盲区根因与教训入 §6）——修正内容与代码事实相符，无虚构锚点。

## Findings

| # | severity | 位置 | 触发条件 | 实际影响 | 证据 | 最小修复方向 |
|---|---|---|---|---|---|---|
| m-1 | Minor | frontend/src/components/terminal/PathPickerDialog.vue:48-52（错误态分支） | WSL 会话 workDir 为 Linux 形态时打开路径选择器，进入形态错误态（listing=null） | 错误文案引导「改用 Windows 盘符路径」，但错误态下面包屑（v-if="model.listing" 才渲染）与一切导航入口均不可用，只剩「重试」与关闭——用户无法在对话框内切到盘符树，只能关闭手输。模型层 navigate 可恢复（pathPickerModel.test.ts:360-377 有测试），纯组件渲染缺口；非回归（HEAD 同场景是静默错位树，更糟），属本批显式化后暴露的体验边界 | 模板 :17-18（面包屑 v-if=model.listing）vs :48-52（错误态仅重试按钮） | 错误态保留可用入口：如显示一个「从主目录开始」按钮（navigate(home)）或路径输入框；不必阻断合并 |

建议区（不影响 VERDICT）：
- PathPickerDialog retry 后 drvfsHint 不刷新（仅打开时拉取一次）——advisory 为进程级累积提示，影响极小。
- drvfsHintFor 匹配方向为「path 在 mountPath 之下」，PathPicker 场景 path（root）可能是 advisory workDir 的祖先——已由 paths 自身 advisory（WorkDir=root 等值命中）自洽覆盖，无缺陷，仅提示匹配语义依赖三入口各自写入。

## 盲区与验证可信度

- 未复跑任何测试/构建（无 bash）：A1 报告自跑证据（go build/vet/test、vue-tsc、vitest 271 过）与静态断言-实现对应复核为替代基座；Go 侧断言与实现逐条核对均自洽，未发现断言失效。
- 未取 HEAD→工作区逐 hunk diff：等价性结论依赖历史报告对 HEAD 语义的引证（p4-evidence.md:42 等）+ 现工作区测试用例全分支覆盖；引证为实施链产物，非独立 HEAD 读码。若 Leader 需要逐 hunk 级确认，需具备 shell 的节点补跑 git diff 核对。

## 结论

VERDICT: PASS_WITH_MINOR
