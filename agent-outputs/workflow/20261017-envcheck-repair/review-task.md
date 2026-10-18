# E4 完整风险审核（diting full，限本阶段增量）

## 目标
审核已集成环境检测/管理修复，给出 Critical/Major/Minor findings + PASS/FIX。只读源代码/测试，报告写入指定文件；不改代码、不提交、不实际安装/卸载软件、不修改真实PATH/profile、不访问密钥。

## 必读输入（以主树为准，不审旧worktree）
- agent-outputs/workflow/20261017-envcheck-repair/workflow.md
- agent-outputs/workflow/20261017-envcheck-repair/shared-contract.md
- agent-outputs/workflow/20261017-envcheck-repair/integration-report.md
- agent-outputs/workflow/20261017-envcheck-repair/backend-report.md
- agent-outputs/workflow/20261017-envcheck-repair/frontend-report.md
- agent-outputs/workflow/20261017-envcheck-repair/backend-integration-findings.md
- agent-outputs/workflow/20261017-envcheck-repair/frontend-integration-findings.md

## 审核范围
- 相对HEAD的 internal/envcheck 增量（含未跟踪新文件，git diff不含它们，必须显式读取）；必要时跟进既有App调用、internal/platform解析器验证边界，但不进行全仓旁支审计。
- frontend/src/views/settings/EnvCheckSettings.vue + envcheckSolutions.ts + envcheckOperationTracking.ts，以及 frontend/src/__tests__/views/settings/ 下对应新测试。
- e2e/envcheck-management.html、envcheck-management-smoke.mjs、envcheck-management.vite.config.mjs。
- docs/api.md、docs/user/faq.md 与 docs/user/usage.md 环境检测段的契约一致性。
- 排除前序独立任务：GeneralSettings/useDefaultEngineSettings、UsageStatsPanel、internal/usage价格更新、usage.md统计段；勿因主树有这些改动扩大本次审核。

## 核心关注
1. 卸载安全：确知npm包和prefix、Homebrew formula真实归属；共享依赖/其他Node版本/未知来源零删除。正常安装能卸且失败命令/探测未知不冒充成功。没有新增Service导出绑定方法或手改wailsjs。
2. PATH修复：写入幂等、规范化、不污染用户配置；当前进程同步与外部终端重启文案一致。Windows分支跨编译边界明确。
3. 状态真伪：未知最新版本不能假报最新；依赖安装退出与复检结果诚实；异步受理不等同成功。
4. UI：快照失败/直接终态/旧槽/响应乱序/长安装、组件卸载、重复点击的忙碌与刷新语义；通用卸载无operation ID回执时的认领和有界未知策略。
5. 测试是否锁真实逻辑，mock是否掩盖公共契约/实际类型；已知未覆盖的真实OS安装不得写成通过。
6. 重点复核一个待证疑点：CheckOne的finishToolCheck吸收error，npm fallback探测失败可能最终返回Installed=false；卸载只判nil/error是否足够？是否需要确认原可执行文件确实已消失，以免隐藏了损坏/权限错误。请基于可复现调用路径裁决，不把猜测当finding。

## 验证
以现有集成证据为基线，可执行隔离定向测试（go test ./internal/envcheck ./internal/platform / npm --prefix frontend run test）；禁止联网补依赖、启动真实CLI安装器、操作真实用户配置。浏览器需要时复用主树e2e替身（只mock绑定，不是实际安装证据）。不用重复执行全仓长测试。

## 产物
写 agent-outputs/workflow/20261017-envcheck-repair/review-report.md：已审核范围、可复核证据、finding具体文件/行号/触发场景/建议、严重度与一行VERDICT。报告应简洁可执行，不为凑问题扩张范围。若无阻断写PASS并保留真实边界。仅在本报告写入范围内修改，不写其他文件。
