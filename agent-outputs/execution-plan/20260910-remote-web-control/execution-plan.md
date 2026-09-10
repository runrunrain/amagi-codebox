# 执行计划：远程 Web 控制界面交付（移动设备可用）

> 生成日期：2026-09-10 ｜ 制定：天城（Leader）｜ 模式：**授权执行**（主上已授权发现即修复/补充迭代；仅契约变更与范围外事项需回报）

## 执行状态（Leader 滚动更新 · 2026-09-10）

| Phase/任务 | 状态 | 备注 |
|---|---|---|
| P0-A 环境基线 | ✅ 完成 | dist 重建、557 基线全绿、pi webui 数据面健康；帧样本改合成（P1-C 落地）；见 P0-基线确认记录.md |
| P1-A 终端仿真面 | ✅ 完成+验收 | 577→601 全绿；只读降级/KeyTray/sendRaw/默认面分支落地 |
| P1-C e2e/回归 | ✅ 完成+验收 | 607 vitest + 135 PG 回归 + 9 workspace-tui e2e 全绿；合成帧 fixture 落地 |
| P1-D 回放竞态修复 | ✅ 完成+验收 | store 级「订阅即回放」原子衔接；611 vitest 全绿（+4 时序回归）；三次 429 重试后换 glm-5.3:max 完成 |
| P2-A 死页清退 | ✅ 完成+验收 | 三死页+TerminalPage(2576行)+legacy api 层清退；越界补丁（a11y-m4 死用例）Leader 已直修 |
| P2-B HostSummary 降级 | ✅ 完成+验收 | 不动 wire：失败降级保守 200（全 CLI 不可启动）；pairing 保持 fail-closed；负例测试绿 |
| P2-C dist 重建+全流程回归 | ✅ 完成+验收 | 构建绿；e2e 241/286，HEAD 基线对照实证：44 遗留 + 1 真回归（ComposerBar 180px）→ Leader 已直修；两裁定项已裁决 |
| P2-D 夹具迁移+终验 | ✅ 完成+验收 | 中断后 Leader 收口：KeyTray 44px 修复+抖动根治；全量 e2e 285 绿 0 失败（HEAD 遗留 44 条全部收复）；dist 11:01 终建 |
| P3-A 自检卡 | ✅ 完成+验收 | 七项自检卡+四横幅+addressRequired 手动兜底+降级提示；frontend 92/92 绿；三次中断后换 glm-5.3:max 完成；后端需求 R1/R2 移交 P3-B |
| P3-B 端口 fallback | ✅ 完成+验收 | 扩为三项：R1 LAN 枚举绑定+R2 降级透出+StartWithRetry 自愈引擎+wailsjs 再生成；App 壳测试 patch Leader 已落盘并补 ctx 注入，root 测试绿 |
| P4 审核/收口 | ✅ 完成 | diting PASS_WITH_MINOR（0C/0Maj/3Min）；3 Minor Leader 直修（lanaddr 注释/降级文案/connection.ts 死代码）；CHANGELOG+用户文档+registry 收口 |
> 输入：`../调研报告/`（主报告 + 素材A~F；本计划行内引用「素材F §6」等均指向该目录）
> 证据基线：amagi-codebox HEAD `f27ad73`（v1.3.65）；锚点符号已实核（RawTerminalView 位于 `mobile/src/components/workspace/RawTerminalView.vue`；特殊键映射在 `mobile/src/views/TerminalPage.vue` `sendSpecialKey`；mobile 已有会话启动器 `SessionsPage.vue:243` + `stores/lobby.ts:356`）。

## 基线

- **目标**：交付优质的远程 Web 控制界面——用户在移动设备（浏览器访问内嵌移动 Web UI）可以：配对连接 → 浏览/启动/管理会话 → **pi/omp 会话如桌面内嵌终端般可读、可交互** → 断线恢复；无必然失败的死页；远程启用有清晰引导与自检；安全门不回退。
- **总验收**：主报告 §8-2/§8-4/§8-5 清单全绿 + 真机走查「开启→LAN 确认→配对→大厅→启动 pi 会话→可读可交互→断线重连补帧」全流程。
- **范围**：mobile 前端渲染/交互达标、死页清理、HostSummary 降级、启用引导自检、产物一致性、阶段审核。
- **非目标（本期不做，另立项/进 backlog）**：桌面 pi Web 平面修复（调研批次 1）、WD-5 配置面 v1 化、remoteclient 桌面互联补缺（远程新建会话 UI 已确认为 mobile 已有、desktop 互联端另议）、Capacitor 原生壳、候选 B（pi webui 远程暴露）、TLS、v1 契约大改（P2-B 涉 wire 语义时按契约同 diff 规则最小化变更）。
- **需求编号**：R1 pi/omp 内容可读（终端仿真）｜R2 pi/omp 可交互（特殊键/组合键）｜R3 pi/omp 默认进终端面｜R4 死页清零｜R5 嵌入产物一致｜R6 远程启用可引导可自检｜R7 安全门不回退｜R8 服务端读面健壮。

## Phase 0：环境基线确认（半天，Leader 直做）

目标：排除旧产物干扰、定谳剩余不确定项、为 Phase 1 采集测试资产。
集成 Gate：`mobile/dist` 重建且桌面嵌入验证通过；远程服务可开启且手机可配对；产出「before 证据」（pi 会话渲染乱码截图/录屏）与 pi 全屏 TUI 真实帧样本。

| ID | 需求 | 完整切片 / Agent | 前置依赖与输入 | 输出 | 验收与验证 | 隔离 |
|---|---|---|---|---|---|---|
| P0-A | R5 | 环境确认与测试资产采集 / Leader 直做 | 调研报告 §5 矩阵、§8-5 | 基线确认记录（落本交付包目录）+ pi 帧样本文件 + before 证据 | `npm run build` 重建 dist；`git status` 无源码改动；手机配对成功；F-5 抽查（本机五 CLI 输入正常）；录制一段 pi 会话 PTY 输出帧样本（供 P1-C） | 否 |

P0-A 步骤要点：① `npm --prefix mobile run build` 重建；② 桌面开启远程控制（RemoteSettings），记录 gate 结果/端口/监听地址；③ 手机配对→大厅→attach pi 会话，留存乱码 before 证据；④ 本机启动 pi/claude 会话各输入一轮（F-5 排查）；⑤ 桌面 PTY tap 或 e2e harness 录 pi 全屏重绘帧样本（素材F 边界项 6）。

## Phase 1：pi/omp 渲染与输入达标（核心，~3 人日）

目标：pi/omp 会话在移动端默认以终端仿真面呈现，ANSI 保真、重绘不堆积、可发特殊键/组合键。
协作：P1-A → P1-C 串行（测试消费实现）；A 内含组件与默认面分支，属同一纵向切片。
集成 Gate：真机验收主报告 §8-2 第 3/4 条（渲染可读 + KeyTray 交互 + 软键盘 refit + gap 提示不伪造内容）。

| ID | 需求 | 完整切片 / Agent | 前置依赖与输入 | 输出 | 验收与验证 | 隔离 |
|---|---|---|---|---|---|---|
| P1-A | R1 R2 R3 | pi/omp 终端仿真面达标（组件升级 + 默认面分支 + 可交互输入）/ luoshen | P0-A 的 before 证据与帧样本；素材F §5/§6（方案与分工边界） | 改动 `mobile/src/components/workspace/RawTerminalView.vue`、`mobile/src/views/WorkspacePage.vue`、`mobile/src/stores/workspace.ts`、`mobile/src/components/workspace/ComposerBar.vue`；实现说明 artifact | 见 Task Contract P1-A | 否（mobile 前端独立域） |
| P1-C | R1 R2 | 全屏 TUI e2e 样例 + 组件/单测回归 / wukong | P1-A 产物；P0-A 帧样本 | `mobile/src/__tests__/` 新增/扩展（raw-terminal 交互、sendRaw、特殊键序列、只读降级）+ `e2e/workspace-pg04.spec.ts` 扩展全屏 TUI 场景 | `npm --prefix mobile run test` 全绿；e2e 新场景过；既有 PG01~04 不回退 | 否 |

### Task Contract P1-A（细则）

- **Target**：
  - `RawTerminalView.vue`：由只读诊断面升级为可交互终端面（保留只读模式作为控制权被夺/观察态降级）。
  - `WorkspacePage.vue`：按 `detail.cliType`（store 已有，`workspace.ts:360`）选择默认视图——pi/omp 默认终端面，其余 CLI 维持 Timeline 分工；`?view=terminal` 语义升级为「pi 的主面」。
  - `workspace.ts`：暴露 `sendRaw(bytes)` 或等价任意字节输入通路（`sendAnswer` 现接受任意字符串，含 `\r`，确认 base64/控制字符链路即可）；restart 边界写本地提示行（对齐 remoteclient 做法）。
  - 特殊键托盘：移植 `TerminalPage.vue` `sendSpecialKey`（:865 起）的序列映射到 v1 通路，补 Esc/Alt 前缀（`\x1b` 系列）。
- **Change（行为要求）**：pi 会话渲染 ANSI 保真（xterm 6 DOM renderer）、全屏重绘帧在屏幕缓冲内正常滚动历史不重复堆积；输入支持普通文本 + Esc/Enter/Tab/Ctrl+C/方向键/Alt 组合（pi TUI 菜单可选中、生成可中断）；gap 提示条（backfill 缺口）不伪造内容；控制权被夺时降级只读并提示；软键盘唤起 refit（组件已有 visualViewport 机制，验证之）；可选增强：进面/resize 后发 `\x0c` 触发 TUI 重绘（素材F 建议，若 pi 实测无副作用则启用）。
- **Acceptance**：组件级 vitest（输入通路/特殊键序列/降级）；真机（或桌面浏览器移动模拟 + 真机各一次）场景：读 pi 会话如桌面般可读、发 Ctrl+C 中断生成、方向键选菜单。
- **Contract（对 P1-C）**：新增组件 props/emits 与 store 方法名稳定；帧样本 fixture 路径约定 `mobile/src/lib/contract/testdata/`（或 e2e fixture 目录，P1-C 落位后回写本计划）；禁用状态语义（readonly/控制权）有显式 data-testid 供 e2e 断言。
- **纪律**：写前先 read 现状；legacy `TerminalPage.vue` 只读取参考不修改（其清退归 P2-A）；不运行项目级全套测试（留 P1-C 与阶段集成）。

## Phase 2：收尾清理与产物交付（4~6 天，P2-A ∥ P2-B → P2-C）

目标：死页清零、v1 读面健壮、嵌入产物与源码一致。
集成 Gate：大厅→启动→attach→pi 可读可交互→死页不可达；桌面端嵌入产物回归通过。

| ID | 需求 | 完整切片 / Agent | 前置依赖与输入 | 输出 | 验收与验证 | 隔离 |
|---|---|---|---|---|---|---|
| P2-A | R4 | 三 legacy 死页下线 + TerminalPage 死代码清退 / luoshen | 素材D §3（Dashboard/Providers/Settings 死因）；P1-A 已迁移特殊键映射 | router 移除/重定向三死页（替换为「请在桌面端管理」引导页或并入设置页提示）；删除 `TerminalPage.vue` 及其独占依赖（`lib/api` legacy 层、legacy ws 层若无引用一并清，有引用则保）；`api/client.ts` Bearer 层清退 | 路由不可达死页；`vue-tsc -b` 过；grep 无 KeyTray 引用残留；死页入口显示引导而非报错 | 否 |
| P2-B | R8 | HostSummary 探测失败降级（读面解耦）/ luban | 素材B §4（fail-closed 单飞缓存致 v1 读面 503）；契约文档规则 | Go：会话列表/详情端点在 HostSummary 探测失败时仍可用（部分数据语义或明确降级错误码，二选一在实现前回报裁决）；**若 wire 语义变化，同 diff 更新** `docs/developer/remote-api-v1-contract.md` + Go/TS 双骨架 + `v1-wire-fixtures.json` + 双测试 | Go 测试：模拟探测失败时会话端点行为；`go vet ./internal/remote/...`；mobile 契约测试同步 | 否（与 P2-A 无文件交集） |
| P2-C | R5 | dist 重建 + 桌面嵌入 + 全流程回归 / wukong | P2-A、P2-B | 重建 `mobile/dist`；移动端全流程 e2e（配对→启动→attach→交互→断线重连）；桌面嵌入产物冒烟 | `npm run build`；PG01~04 + 新 e2e 全绿；桌面侧远程设置页无 500 | 否 |

## Phase 3：启用体验自检（3~5 天，可与 Phase 2 并行启动 P3-A）

目标：远程控制「默认关闭 + 回环监听 + 二维码空地址」的启用门槛变为可引导、可自检。
集成 Gate：默认配置全新走查「开启→LAN 确认→配对→终端工作」≤5 步且每步有状态反馈。

| ID | 需求 | 完整切片 / Agent | 前置依赖与输入 | 输出 | 验收与验证 | 隔离 |
|---|---|---|---|---|---|---|
| P3-A | R6 | 远程设置自检卡与配对地址修复 / luoshen（少量 Go 绑定可与 luban 协作） | 素材B §3/§4（启停链、addressRequired）；`RemoteSettings.vue` 现状 | 自检卡：服务运行状态、gate 警告透出、监听地址/端口、端口占用提示；配对二维码地址兜底（LAN 地址枚举供用户选择，替代空地址）；移动端访问地址一键复制 | 真机走查 ≤5 步；addressRequired 场景二维码可用；自检项与真实状态一致（停服/占用端口负例） | 否 |
| P3-B | R8 | 端口占用 fallback 与 enabled/running 漂移自愈 / luban（条件执行） | 素材B ⑥（Startup 恢复路径仅 Warn） | 若 P3-A 验证发现确需：端口被占时的明确报错/换口提示与 enabled 状态自愈 | 单测覆盖占用/漂移两场景 | 否 |

## Phase 4：阶段审核与收口（diting 一次）

| ID | 需求 | 完整切片 / Agent | 前置依赖与输入 | 输出 | 验收与验证 | 隔离 |
|---|---|---|---|---|---|---|
| P4-A | R7 | Phase 1~3 全量 diff 审核 / diting | 阶段全部实现与集成验证完成后的 diff | 审核报告（findings 分级） | 安全回归：legacy 面仍拒非回环、v1 Host/Origin/query 门保持、错误 token/Cookie 仍 401/403、设备撤销即时生效（主报告 §8-4）；契约一致性（若 P2-B 动了 wire，四件套齐） | 否 |
| P4-B | — | findings 处置与收口 / Leader 直修（多则合并一个修复批次） | P4-A | 修复+回归；CHANGELOG 条目与 `docs/user/remote-mobile.md` 更新（cangjie/taibai 按触发） | 全部 Critical/Major 闭环；真机终验 | 否 |

## 专家预算

| 父任务/大阶段 | fuxi | diting | 触发证据 |
|---|---|---|---|
| 远程 Web 控制界面交付（全程） | 0 | 1（Phase 4） | 素材F 已提供渲染方案方向，无需 fuxi；远程安全面为高风险大阶段，实现完成后一次 diting |

## 风险与回滚

| 风险/触发条件 | 影响 | 预防、验证或回滚 |
|---|---|---|
| pi 全屏重绘帧构成为推断（素材F 边界 6） | P1-A 渲染效果不达预期 | P0-A 先录真实帧样本；xterm 全仿真对重绘天然正确（桌面已证），风险主要在性能而非正确性 |
| iOS 软键盘与 xterm 焦点兼容未验证 | 真机输入卡顿/焦点丢失 | P1-A 验收含真机；legacy TerminalPage 曾实现可参考；失败则参考其 focus 管理补丁 |
| 重绘负载下手机性能 | 掉帧/OOM | DOM renderer + 适度 scrollback 上限（如 5k~1w 行）；screenReaderMode 不默认开 |
| P2-B 触碰 wire 语义 | 契约漂移、双端不同步 | 契约四件套同 diff 硬规则；实现前回报裁决；独立 commit 可单独回滚 |
| 控制权语义（终端面交互与单控制者仲裁冲突） | 双端同时输入竞争 | P1-A 沿用 outbox 控制权过滤（P-04 语义不变）；被夺降级只读为验收项 |
| mobile/dist 再度过期 | 嵌入旧 UI | P2-C 后把 `npm run build` 纳入交付 checklist（主报告 §8-5） |
| Phase 1 发现现状与素材F 证据不符 | 计划前提失效 | 授权范围内即查即修；实质偏差回报主上并修订计划 |

## 需求追溯

| 需求 ID | 任务 ID | 验收证据 |
|---|---|---|
| R1 pi/omp 可读 | P1-A、P1-C | 真机渲染走查 + e2e 全屏 TUI 场景 |
| R2 pi/omp 可交互 | P1-A、P1-C | 特殊键/组合键真机操作 + vitest 序列断言 |
| R3 默认终端面 | P1-A | cliType 分支单测 + 走查 |
| R4 死页清零 | P2-A | 路由不可达 + 引导页展示 |
| R5 产物一致 | P0-A、P2-C | dist 重建时间戳 + 嵌入冒烟 |
| R6 启用引导 | P3-A | ≤5 步真机走查 + addressRequired 场景 |
| R7 安全不回退 | P4-A | diting 安全回归 findings 清零 |
| R8 读面健壮 | P2-B（P3-B） | 探测失败负例测试 |

## 执行模式备忘（主上授权语义）

- 发现即修复：各切片执行中发现的明确小缺陷（本切片文件域内）直接修并在报告中列出；跨域/契约/超出非目标边界的，回报裁决。
- 阶段推进：每 Phase Gate 由 Leader 验收后进入下一 Phase；P2-A/P2-B 并行、P3-A 可与 Phase 2 并行。
- 计划本身可修订：执行中如需调整切片/依赖，Leader 直接更新本文件并在变更处标注日期。
