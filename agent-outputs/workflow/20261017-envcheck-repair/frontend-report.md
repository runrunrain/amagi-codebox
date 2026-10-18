# E2 前端切片报告：环境检测管理按钮 / 异步操作 / 状态刷新

- 基线：HEAD 7867cc3 隔离 worktree（amagi/envcheck-frontend-repair）；本文档含两轮：初版修复 + 集成回派（frontend-integration-findings.md，3 Major + Minor）
- 净增量（当前）：
  - `frontend/src/views/settings/EnvCheckSettings.vue`（修改，+474/−156）
  - `frontend/src/views/settings/envcheckSolutions.ts`（新增，决策层）
  - `frontend/src/views/settings/envcheckOperationTracking.ts`（新增，受理跟踪状态机）
  - `frontend/src/__tests__/views/settings/envcheckSolutions.test.ts`（镜像测试）
  - `frontend/src/__tests__/views/settings/envcheckOperationTracking.test.ts`（可运行状态逻辑测试）
- 未改：wailsjs、后端、GeneralSettings、usage、主树 e2e；无新依赖；未提交 git
- 环境备注：worktree 内 `frontend/node_modules` → 主树 frontend/node_modules 符号链接（gitignored，portable 模式），不入 diff

## 第一轮根因（初版，证据见代码注释锚点）

| # | 根因 | 症状 |
|---|---|---|
| R1 | `restart_app` solution 透传 RunEnvFixAction，后端白名单不受理 | 「重启应用」按钮永远报错 |
| R2 | `install_claude_method` 经 RunEnvFixAction 丢 method、无进度无刷新 | 重装按钮无反馈、状态陈旧 |
| R3 | 修复成功后不刷新/不轮询（changed=false 分支丢后端重检结果） | 「PATH 修复无效」 |
| R4 | `requiresRestart` 未展示 | 用户误读修复无效 |
| R5 | 快速异步操作终态漏报（watch 只覆盖 running→终态） | 秒完成操作无反馈 |
| R6 | 顶层 `status.solutions` 从未渲染 + 安装按钮不读 `canInstall` | dependency 夹具：无依赖按钮、必失败安装仍可点 |
| R7 | 四 Agent CLI（opencode/codex/pi/omp）无卸载入口 | 已装 CLI 无法卸载 |
| R8 | uninstall_tool 受理若按 success 即成功会误报 | 假卸载成功（契约正确落地） |

## 集成回派修复（frontend-integration-findings.md）

### Major 1：受理回执接管状态 / 快照失败与乱序（smoke 第5项失败根因）
- 新增 `envcheckOperationTracking.ts` 状态机：受理回执（Start* 的 OperationState id；uninstall_tool 无回执 → id=''，以 tool+kind+时间界匹配，startedAt/finishedAt ≥ 受理时刻−2.5s，running 态直接认领）是操作身份起点。
- `fetchSnapshot` 无论成败都喂跟踪器：**失败观察（null）不清 pending**；`ensurePollingState` 条件加 `opTracker.pending !== null` → 快照持续失败期间轮询不中断（snapshot-error 场景根因），45s 有界超时兜底。
- 终态即使没见过 running（快操作/乱序）也产出 terminal 事件；`resolvedOpIds` + `isEffectivelyRunning` 防乱序快照把已终结操作回退成 running。
- 旧卸载终态（startedAt 早于受理）不认领 → 走超时兜底「状态未知」：warning 横幅 + 自动单检 + 提示手动检测（不误报、不无限等）。
- `runningOperation` computed：服务端 running 态优先，快照缺 operation/拉取失败时由 pending 合成视图接管（进度沿用最近观察值，不闪回 0）。

### Major 2：统一互斥 + 稳定 solution key
- `writeBusy` 全局写 guard（handler 内 guard，非仅 template）：`withWriteGuard` 包裹 executeSolution 全部写计划与模板直连入口（onInstallClick/onUpdateClick/onClaudeInstallClick/onUninstallClaudeClick/onUninstallHeadroomClick/onFixClaudeConfigClick）；受理窗口内二次点击直接忽略；成功/失败都在 finally 释放；后续忙碌由 pending/runningOperation 接管，无空窗。
- 稳定 key：`solutionIdentity = type|tool|method|command`（无位置下标），模板 issue/顶层按钮与 executeSolution 共用 `solutionActionKey` —— 顶层与非 0 下标按钮的「处理中…」与禁用不再错位。
- `isWriteSolution`：manual_command / restart_app 非写操作，不受 busy 屏蔽（纯展示保持可用）。

### Major 3：修复后确定性复检
- `planPostFixRefresh` 重写为 `{ recheck: 'none' | 'snapshot' | 'full' }` 并真正消费：失败 → none（错误横幅即重试入口）；成功 changed=false → snapshot（后端分支内已同步重检）；成功 changed=true → **full：本端 `await runEnvCheck()` 确定性全量复检**，不再依赖「刚好碰到 status.checking=true」的窗口；复检失败提示手动「检测」重试，绝不无限轮询。

### Minor：文案与数据身份
- restart_notice 与 requiresRestart 文案改为条件式（「若界面仍显示旧 PATH：重启 CodeBox 后生效；外部终端请重启终端或 source 配置文件」），不再宣称「运行中无法刷新」（E1 会同步刷新当前进程 PATH）。
- solution 身份/dedupe 键补 tool；`visibleTopLevelSolutions(status, cardTool)` 以卡片 tool 作为无 tool 副本回退（后端双挂副本要么都带 tool 要么都不带，混合形态仍去重、不同卡片不合并）。

## 可复现步骤（本工作树用户入口走查，mock Wails，临时 devmock 已删除）

入口：worktree vite(5199) + `frontend/devmock.html`（mock window.go：快照失败窗口/慢受理/卸载状态机，confirm 自动接受）。

- **Major 1 snapshot-error（对应 Leader smoke 第5项）**：OpenCode → 卸载 → 浏览器侧采样 t0/+1s/+2.2s/+4.8s：
  - t+1s、t+2.2s（快照持续失败中）：卡片「卸载中」、横幅「卸载进行中」、hero「OpenCode 卸载中」、卸载按钮 disabled「卸载中...」——pending 未丢、轮询未断；
  - t+4.8s（快照恢复）：横幅「OpenCode 卸载成功」、卡片翻「未安装」——全程未见过 running，终态直接确认。
  - 截图：`/tmp/codebox-envcheck-fix2-snapshot-error-running.png`、`/tmp/codebox-envcheck-fix2-uninstall-recovered.png`
- **Major 2 处理中/双击**：Pi → 点顶层「安装 Node.js」（慢受理 1.5s）：按钮变「处理中...」且 disabled，「安装」「检测」同时 disabled；连点两次仅产生 **1 条** `RunEnvFixAction:install_node`。
- **Major 3 确定性复检**：接上，install_node 成功（changed=true）后调用日志出现 **1 条 RunEnvCheck**（本端确定性复检）。

## 验证

| 项 | 命令 / 步骤 | 结果 |
|---|---|---|
| 可运行状态逻辑测试（Leader 要求） | `cd frontend && ./node_modules/.bin/vitest run src/__tests__/views/settings/` | 3 文件 45/45 通过，exit 0；其中 envcheckOperationTracking.test.ts 按真实时序喂序列：snapshot-error 不清 pending、无 running 直达终态、旧卸载终态不误报（时间界）、有界超时 unknown、乱序防回退、进度透传 |
| 决策层镜像测试 | 同上（envcheckSolutions.test.ts） | 覆盖 R1–R8 + 稳定 key + isWriteSolution + cardTool 回退去重 + 确定性复检策略 + 条件式重启文案 |
| frontend build（vue-tsc 门禁 + vite） | `cd frontend && npm run build` | exit 0 |
| 用户入口交互（mock 后端） | agent-browser + devmock（真实组件交互链） | 上述 Major 1/2/3 三组证据 + 首轮 R1–R8 走查（见下） |
| 不触及真实设备 | 全程 mock window.go；未装/卸软件、未改 PATH/用户配置、未读密钥 | — |

首轮走查截图（本机 /tmp，首轮 devmock）：`/tmp/codebox-envcheck-dependency-after.png`、`/tmp/codebox-envcheck-uninstall-before.png`、`/tmp/codebox-envcheck-uninstall-running.png`、`/tmp/codebox-envcheck-uninstall-done.png`。

## 边界与未覆盖

- **uninstall_tool 后端尚未落地**（E1 范围）：当前后端会拒绝该动作，前端按「受理失败」如实报错；E1 合入后自动走完整受理→终态链路（契约按 shared-contract.md 实现，mock 状态机已验证两条时间线）。
- 组件 DOM 渲染层（writeBusy 禁用面、处理中文案）无 jsdom 挂载测试（vitest node-only、无 @vue/test-utils）；由上述浏览器走查 + Leader 主树 10 项 harness 覆盖。
- restart_notice 条件式文案改动在组件层（无单测），代码已按 E1 契约校准；建议 Leader harness 的 restart 场景顺带核对文案。
- 45s pending 超时窗口为前端兜底常量（PENDING_OP_TIMEOUT_MS）；若后端存在 >45s 的合法长操作且快照长期不可见，会出「状态未知」并自动单检——按「不无限轮询」要求的有界取舍。
- 未做全仓回归（Leader 统一运行）；`fix_claude_config` solution 后端 checker 不发出，保持透传不动。
