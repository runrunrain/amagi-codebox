# E2 前端工匠闭环（complex）

必读 /Users/run.mao/workpace/amagi-codebox/agent-outputs/workflow/20261017-envcheck-repair/workflow.md 与 CLAUDE.md。
用户反馈五个Agent环境检测管理异常：检测已安装失败、安装卸载失败/状态异常、依赖按钮无效、PATH修复无效。负责 UI→现有绑定调用→异步操作/错误反馈/刷新 这一独立切片，查证后修复，不猜后端新协议。

调查 frontend/src/views/settings/EnvCheckSettings.vue 及环境检测关联components/composables/api，可只读后端 internal/envcheck/types.go service.go fix_dispatcher.go installer.go、app.go。核对action id映射/autoFix、依赖按钮被隐藏/禁用、RunFixAction成功与异步操作区分、operation polling失败/终态复检、五CLI安装来源卸载按钮。后端另有Agent修复；保持现有契约，需契约变化先反馈。

写入：环境检测前端源及其镜像位置测试。禁止改wailsjs/后端/GeneralSettings/usage，禁止新依赖或提交git。HEAD=7867cc3隔离工作树，前序其他改动刻意不带入。

禁止真实设备软件安装/卸载、PATH或用户配置修改，不读密钥；用mock后端验证。如浏览器走查必须read agent-browser技能，开发环境mock Wails入口，不点击真实后端管理操作。

验收：根因回归测试、定向vitest、frontend build。尽量用户入口交互验证并写真实步骤和截图路径，做不到明确边界。不要全仓回归（Leader统一运行）。报告写 /Users/run.mao/workpace/amagi-codebox/agent-outputs/workflow/20261017-envcheck-repair/frontend-report.md，列根因/修改/可复现步骤/验证/净增量/边界；终稿短摘要，不粘贴长报告。
