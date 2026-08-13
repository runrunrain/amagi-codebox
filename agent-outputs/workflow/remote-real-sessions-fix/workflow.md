# 目标与验收

- 目标：让远程 Web 会话大厅展示并操作宿主真实终端会话，并确保 Web 启动走与桌面端一致的真实会话创建与 PTY 生命周期。
- 总验收：桌面端已运行的 Claude Code、OpenCode、Codex、Pi、Oh My Pi 会话能出现在 Web 列表；Web 启动会话进入同一 session.Manager、使用真实宿主配置并可被桌面/Web 同步观察和控制；Go 与 mobile 构建通过；关键链路有针对性回归测试；接口与控制安全审核通过。

# 阶段与步骤

## Phase 1: 证据化定位与修复边界

- 步骤 1.1: 追踪移动端到远程服务再到宿主会话管理器的完整链路
  执行: baize（fast）
  载体: SubAgent
  输入: 用户截图与当前代码
  产出: 全局 artifact store 中 baize 任务报告
  验收: 相关文件、符号、行号、根因和测试缺口齐全
  验证: 无（只读探索步）
  Gate: 根因有代码证据且能解释两个用户现象
  状态: done（证据：agent-outputs/baize-lobby-session-disconnect.md）

- 步骤 1.2: 确定单一真相源与最小生产级修复接口
  执行: fuxi（expert）
  载体: SubAgent
  输入: 用户截图与当前代码
  产出: 全局 artifact store 中 fuxi 任务报告
  验收: 明确会话投影、真实启动、生命周期和兼容边界
  验证: 无（架构分析步）
  Gate: 修复接口能复用真实桌面会话路径且不绕过 ControlGate
  状态: done（证据：/Users/maorun/maorun-workpace/projects-memory/projects/amagi-codebox/agent-outputs/architect/20260804-remote-session-truth-source-audit/design-doc.md）

## Phase 2: 实现与针对性测试

- 步骤 2.1: 统一真实会话目录、启动路径与远程输出生命周期
  执行: hongjun（expert）
  载体: SubAgent
  输入: Phase 1 结论与当前代码
  产出: changed files + 全局 artifact store 中 implementation report
  验收: 宿主现有会话可投影到 Web；Web 启动写入真实 Manager 并走宿主配置/PTY；状态、停止、删除、输出链路一致
  验证: L1 Go 编译/冒烟 + L2 真实会话投影和远程启动针对性测试
  Gate: 实现与测试通过，且无假会话路径
  状态: done（SessionAuthority、composite activation/remove/restart、exact processcap/PTY readiness、五 CLI Planner/Executor、shared lease、config CAS debt 均已接线；证据：agent-outputs/hongjun/remote-real-sessions-fix-report.md）

- 步骤 2.2: 校验并按需修正 Web 大厅状态呈现
  执行: luoshen（work）
  载体: SubAgent
  输入: 后端接口实际行为与 mobile 当前实现
  产出: changed files（如需）+ 全局 artifact store 中前端验证报告
  验收: 3 个宿主会话显示为 3；刷新/启动/进入会话关键状态正确，无占位或伪成功
  验证: L1 mobile build + L2 store/component 测试 + 浏览器真实交互验证
  Gate: 前端对真实 API 行为无错误过滤或伪造
  状态: done（无需修改 mobile/frontend；mobile unit/build、frontend build 与 workspace-real Playwright 4/4 PASS；证据：agent-outputs/wukong/remote-real-sessions-validation.md）

## Phase 3: 审核与终验

- 步骤 3.1: 独立代码审核
  执行: diting（expert）
  载体: SubAgent
  输入: Phase 2 diff 与测试证据
  产出: 全局 artifact store 中 review report（含 diff 基线）
  验收: VERDICT PASS；Critical/Major 回流修复
  验证: 审核接口、控制权、生命周期并发和测试风险映射
  Gate: 审核通过
  状态: done-with-findings-closed（完整终审与唯一增量复审均为 FAIL；两轮报告的 Critical/Major 已在最终闭环批次逐项修复。按审核循环上限不再发起第三轮 diting，改由 Leader 静态核对 + wukong 独立全量/race/Playwright 终验）

- 步骤 3.2: Leader 端到端终验与需求对照
  执行: Leader（leader）
  载体: Leader 直做
  输入: 实现、测试、审核报告
  产出: 最终回复
  验收: 初始两个问题均判定 met，列明 changed files、验证命令和回滚方式
  验证: L1 Go/mobile build；L2 相关 Go/mobile 测试；可运行环境下浏览器交互
  Gate: 可交付
  状态: done（Leader 隔离 HOME 全仓 Go 测试、定向生产事务测试、vet、diff check、mobile/frontend build 通过；真实 Windows/五 CLI 用户凭据 smoke 受当前平台与安全边界限制，见最终报告）

# 风险标注

| 步骤 | 风险点 | 必须过 diting 审核位的理由 |
|---|---|---|
| 2.1 | 公开 API、远程控制权限、PTY 生命周期、并发状态 | 影响远程会话读写和宿主进程控制，属于接口与权限高风险路径 |
| 2.2 | Web API 契约与真实操作反馈 | 防止前端再次出现伪成功或错误状态投影 |

# 自治级别

interactive

# 修订日志

- 2026-03-26 创建 workflow；根据用户截图将会话真相源与真实启动列为同一修复目标。
- 2026-03-26 架构审计淘汰双向补双写，Phase 2.1 改由 hongjun 按 SessionAuthority 方案执行。
- 2026-03-26 实现前审核发现 H1/H3 composite commit、remove receipt、membership attach、process binding/journal GC owner 边界；经补遗与复审后冻结唯一实现合同再开工。
- 2026-08-09 后端 P0 安全收敛完成并通过隔离 HOME 全仓测试、race、vet 与 Windows/Linux 编译。
- 2026-08-09 五 CLI Planner/Executor、exact PTY ready/bootstrap、Authority restart、config CAS debt 与 exact-generation shared lease 完成；wukong 独立终验判定三个用户目标 MET。
