# E1 后端工匠闭环（complex）

必读 /Users/run.mao/workpace/amagi-codebox/agent-outputs/workflow/20261017-envcheck-repair/workflow.md 和 CLAUDE.md。
用户反馈五个Agent环境检测管理异常：不能识别已安装、安装/卸载不成功、状态异常、依赖无法按钮安装、PATH修复无效。先定位确证根因，再实施回归修复；避免无证据重构。

重点 internal/envcheck 所有 checker、installer、fix_dispatcher、npm候选/缓存/异步service，internal/platform executable/path/process runner，app.go环境管理绑定。检查 GUI PATH 与 fnm/nvm/Homebrew/npm/native 解析，安装目标和卸载目标一致，退出码/复检/缓存失效，Node/Python等依赖动作可达，PATH落盘及当前进程环境一致。macOS/Windows/WSL 保持跨平台可编译。发现前端问题报告，不越界修。

隔离基线 HEAD=7867cc3；主树前序GeneralSettings/usage改动不在worktree，是刻意的无关范围隔离，不需要带入。
写入：internal/envcheck/**、相关 internal/platform/**、必要app环境入口/对应测试。禁止写前端/wailsjs/usage，禁止新Go依赖、自行提交。保持公共绑定契约；必需新增导出字段/接口先报告Leader。

安全：可以真实本机只读查找，不执行真实安装/卸载/更新，不修改用户shell启动文件/PATH/全局配置，不读取密钥。使用t.TempDir/注入runner/临时fake executable验证管理操作。

验收：新增测试复现并锁定根因；go test ./internal/envcheck ./internal/platform，相关race测试，Windows编译检查。不要运行全仓回归，Leader集成统一执行。报告 /Users/run.mao/workpace/amagi-codebox/agent-outputs/workflow/20261017-envcheck-repair/backend-report.md，列根因、复现、修改、真实验证、剩余边界以及任务基线净改动；重大阻断立即反馈。普通摘要不超过10条，长证据放报告。
