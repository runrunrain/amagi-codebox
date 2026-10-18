# 环境检测与管理链路修复

## 目标与边界
- 用户反馈：五个 Agent 的已安装检测、安装、卸载、状态、依赖按钮、PATH 修复不可靠。
- 总验收：各操作使用一致的可执行文件/安装来源；不能把 PATH 未命中当未安装；安装/卸载依据实际复检结果；依赖缺失有可用操作或明确原因；PATH 修复可验证、幂等且不破坏用户配置；界面不显示虚假成功或陈旧状态。
- 平台：本机 macOS 实测只读探测；Windows/WSL 分支通过隔离测试与编译验证，不声称实机通过。
- 不修改真实用户配置、不卸载本机软件、不安装全局依赖、不提交 Git。保留任务开始前的 GeneralSettings/usage 未提交修改。
- 自治：interactive；有破坏性真实环境动作必须另行确认。

## 阶段与任务
| ID | 切片 / 负责人 | 输入与依赖 | 写入范围与产物 | 验收 | 状态 |
|---|---|---|---|---|---|
| E1 | 后端检测/安装卸载/依赖/PATH 根因修复，luban（c41d185e → 9531a896） | 用户反馈、internal/envcheck、internal/platform、App 管理入口；无 E2 依赖 | 隔离 worktree：internal/envcheck、相关 internal/platform、必要 App 入口与测试；backend-report.md | 可复现回归用例；定向测试/-race；Windows 编译；无真实安装卸载 | 完成并接受（含Leader补修）；最终主树验证与E4通过 |
| E2 | UI 管理按钮/异步操作/状态刷新根因修复，luban（33093cc2 → dd8f40d4） | 用户反馈、EnvCheckSettings、API 包装与 Wails 现有契约；无 E1 依赖 | 独立 worktree：环境检测前端源/测试；frontend-report.md | 按钮调用与错误/忙碌/成功状态测试；类型检查与构建；用户入口交互验证 | 完成并接受（含Leader补修）；15场景smoke、327单测、build及E4通过 |
| E3 | 集成与用户入口验证，Leader | E1/E2 报告与净增量 | 主树选择性合并；integration-report.md | Go 定向测试、race、frontend test/build；真实 UI 入口（后端替身禁止真实软件管理） | 首审补修后再次全部通过（bg-703550de / bg-a7d929e4；15场景） |
| E4 | 风险审查，diting full | E3 完整 diff 与验证 | review-report.md，只读 | 审核 PATH 写入、卸载范围、进程退出码、异步状态；Critical/Major 清零 | 完成并接受；首审2 Major关闭，91f7c414增量复审PASS |

## 跨边界链路
| 用户入口 | 调用链与关键状态 | 失败路径 / 验收责任 |
|---|---|---|
| 环境检测/刷新 | EnvCheckSettings → Wails App/Service → checker/process runner → CheckStatus/cache → UI | 环境不可见≠未安装；检测失败与版本未知分开；E1/E2，E3集成 |
| 安装/更新/卸载按钮 | UI → Start* / RunFixAction → installer → process runner → 复检 → operation terminal state → UI refresh | 不能仅按 Promise fulfilled 或 exit=0 报成功；串行与超时显式；E3 |
| 依赖安装/修复 PATH | solution action → fix dispatcher →平台安装/配置写入→重新构建环境→复检 | 依赖安装不可达要明确原因；PATH 修复必须落盘+当前环境生效，失败保留证据；E3 |

## 隔离与共享契约
- E1/E2 均以 HEAD (7867cc3) 隔离 worktree 开发；前序未提交修改与环境检测无交集，留在主树，集成只采纳任务净增量，禁止整树覆盖。工作流文件通过绝对路径读取。
- 既有 Wails 接口/返回结构优先保持；若需新增字段或公共契约改动，先报告 Leader 决定并同步，不手改 wailsjs。
- package 安装/卸载与 PATH 修改只在临时目录/注入 runner 中验证，不执行真实系统管理命令。

## 风险与修订
- PATH 来源多（GUI、login shell、fnm/nvm/Homebrew/npm/原生/WSL），检测与实际执行必须一致。
- 现有不少测试仅 stub 成功路径，必须增加失败路径与实际文件布局夹具。
- 2026-10-17：建立工作流；未预判根因，E1/E2 各自调查后实现，证据缺口在报告中保留。
- dirty-snapshot 创建被工具拒绝（ignored-file 错误；.amagi-worktrees 在 .gitignore 内），两单 effects=none，未建分支或改主树。改为显式 HEAD 隔离基线，而非回落共享工作目录；前序改动在集成时仍保留。
- 已确认4个非Claude Agent缺少通用卸载：共享协议详见 shared-contract.md，沿用RunEnvFixAction新增动作字符串，无新增绑定。
- 修复前Go两包基线全绿；真实UI替身重现详见 ui-baseline-evidence.md。E2初稿真实smoke前4项通过，第5项首次快照失败恢复超时；另有busy/key和修复后复检窗口问题，统一记录 frontend-integration-findings.md 并resume一次。旧E2已reject，由修订候选替代。E1初稿中导出方法/卸载归属边界与报告不一致，统一记录backend-integration-findings.md后reject并续接。鉴于新增自动卸载的破坏性边界，E4升级为full覆盖（仍只审本阶段相关diff）。
