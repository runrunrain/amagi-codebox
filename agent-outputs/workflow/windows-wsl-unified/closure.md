# Windows / WSL 统一管理交付

## 结果
实现已合入主工作区，未提交（主分支HEAD保持0723bdb）。独立初审3 Major/1 Minor形成单批修复，唯一一次增量复审PASS，F1–F4全部关闭。真实Windows/WSL验收仍未执行。

## 用户入口与行为
- 环境检测 → WSL内的CLI → 配置状态 → 启用统一管理：一次明确授权后，在兼容范围内由Windows统一配置，WSL仍运行Linux CLI与兼容副本。
- 五CLI（Claude Code/OpenCode/Codex/Pi/OMP）应用内安装与升级：该CLI选择WSL模式或已在WSL原生安装时，联动现有默认有效发行版；不遍历或修改所有发行版。WSL卡片已装按钮真正走升级，不再幂等短路。
- Windows/WSL分别报告结果，部分失败warning；终态后兄弟WSL卡片自动刷新，可单独重试WSL。
- 配置启动补漏/保存推送/启动对账；技能与资源后台每5分钟重扫。活动会话挡住的推送在会话结束后按5秒心跳补推。停机不唤醒；停用/Shutdown收敛循环。
- per-generation授权、unknown拒绝、手改冻结、非托管同名保护、detached停跟随、敏感白名单与原权限机制保持；部分初始化失败自动续接。选择集物化明确键，不引入旧版本误删风险的星号选择协议。

## 总验收
| 条目 | 状态 | 证据/边界 |
|---|---|---|
| R1统一配置 | met（兼容范围内）/partial（非任意配置全量互通） | App入口+真实后台loop测试；Pi设置、Claude/Codex安全键与可移植资源、原有provider启动写入支持。下述未支持类型不宣称覆盖 |
| R2安装/升级联动 | met（代码与接缝验证） | 五CLI映射、已装升级、sync/async/fix/Claude指定通道；真机执行未验证 |
| R3双侧结果可观察 | met | warning/skip/success与跨卡片刷新，24个真实Vue隔离fixture交互 |
| R4安全保护 | met（逻辑回归） | 授权/取消/换代/手改/detached回归；UNC/9P真实权限未验证 |
| R5验证与审核 | met（本机）/partial（实机） | 全量Go、race、vet、Windows build、frontend build/413 tests、24 browser；独立复审PASS |

## 明确未覆盖
- 实际wsl.exe调用、Linux npm安装升级与真实CLI模型消费、9P权限窗口、超时后Linux子进程清理、真实大规模性能。
- OpenCode用户全局附加层之外配置、OMP models.yml之外配置、Claude/Codex订阅OAuth、全引擎插件迁移。五CLI安装升级覆盖不等于五CLI任意配置全量兼容。
- 停用不撤回既有授权或删除副本，已授权内容仍可能由原有机制/lazy更新。

## 验证与产物
- verification.md：命令、日志、失败测试命令/fixture修正的完整记录。
- review.md §7：最终PASS及独立四个真实loop用例各三轮race证据。
- workflow.md：任务图、回执接受与修订。
- research.md、direction.md、setup-implementation.md、config-implementation.md、install-implementation.md、review-fix.md：调查、设计与实现来源。
- 文档：docs/user/installation.md；docs/developer/wsl-cli-install-sync.md。

## 改动主面
App编排：app.go、app_wsl_supply.go、app_wsl_unified*.go、app_wsl_cli_sync*.go。
领域：internal/wslsetup/**（五CLI/UpdateTool/可移植runner与测试）、internal/wslsupply/**（统一授权/显式选择/扫描回调）、internal/envcheck/**（全入口联动/结果）。
前端：EnvCheckView.vue、EnvCheckSettings.vue、WSLCLISettings.vue、envcheckSolutions.ts、api/wslSupply.ts、api/wslsetup.ts与单测；官方生成器再生frontend/wailsjs/go/*。
交互回归：e2e/envcheck-management*.{html,mjs}、e2e/wsl-cli-supply-ui.html、e2e/wsl-unified-management-smoke.mjs。

## 工作区与清理
保护原有未跟踪归档，未执行提交或整文件回滚。所有worktree保留未提交用于追溯，不自动删除脏树。两台本次创建的Vite fixture服务已停止，browser测试具名session已关闭。绑定生成使用临时HOME，无需访问或写入真实用户配置。
