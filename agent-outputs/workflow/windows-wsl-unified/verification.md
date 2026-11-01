# 集成验证记录

当前状态：实现、集成验证及独立审核已收口。初审3 Major/1 Minor全部关闭，唯一一次增量复审PASS；不代表Windows实机通过。

## 已执行
- 实现前基线：`go test ./internal/wslsetup ./internal/envcheck ./internal/wslsupply ./internal/launcher`，exit 0（baseline-tests.log）。
- 实现前前端：`npm --prefix frontend run build`，exit 0（baseline-frontend.log）。
- P2-A集成：`go test -race ./internal/wslsetup -count=1` + `GOOS=windows go test -c -o /tmp/codebox-wslsetup-unified.test.exe ./internal/wslsetup`，exit 0（setup-tests.log）。
- P2-A阶段全量：`go test ./...`，exit 0（setup-full-go.log）；此时P2-B/P2-C尚未合入，不算最终全量。
- P2-C集成：`go test -race ./internal/envcheck . -run 'WSLSync|SyncCLIToWSL|UpdateCLIInWSL' -count=1`，exit 0（install-tests.log）。
- 真实Vue安装设置页隔离fixture：`node e2e/envcheck-management-smoke.mjs`，18项通过（browser-install.log），包括Windows成功/WSL成功、WSL失败warning、跳过原因和既有安装/卸载跟踪回归。浏览器由agent-browser具名session驱动，Wails后端是明确的内存替身，绝不声称执行真实Windows/WSL安装。
- P2-C绑定由官方Wails生成路径 `go build -tags bindings` + 临时HOME内运行二进制生成（bindings.log）；未手改生成目录，最终P2-B合并后还需再生成。

## 最终集成验证
- `go test ./...` exit 0（final-go-tests.log）。
- `go test -race ./internal/wslsupply ./internal/wslsetup ./internal/envcheck . -run 'Unified|WSLSync|SyncCLIToWSL|UpdateCLIInWSL' -count=1` exit 0（final-race.log）。wslsetup此过滤没有测试，底层包完整race已在setup-tests.log单独通过。
- `go vet ./...` exit 0（final-vet.log）；`GOOS=windows go build ./...` exit 0（final-windows-build.log）。
- 官方bindings生成器在临时HOME中再生成（bindings-final.log），恢复生成器额外改动的runtime文件权限到原644，未手改生成内容。
- `npm --prefix frontend run build` exit 0（final-frontend-build.log）。
- `npm exec -- vitest run` cwd=frontend exit 0，41文件/413测试（final-frontend-tests-corrected.log）。首次误用 `npm --prefix frontend exec -- vitest run` 的子进程仍在根目录，未加载frontend配置而扫描到worktrees/Playwright测试，失败记录保留 final-frontend-tests.log；定位cwd后按正确项目命令重跑，不作为产品失败或通过证据。
- `node e2e/wsl-unified-management-smoke.mjs` exit 0，6项通过（browser-config-final.log）：明确启用→状态刷新、停用、已装升级走Update、OMP安装、授权报错、升级报错。前两次脚本失败已保留：首次错误地让fixture返回failed数组而真实Go入口会返回error；修正错误形态后又暴露页面readiness等待不够，改为等待真实button与fixture数组，最终通过。没有修改生产逻辑来迎合fixture。

## 审核收口
- 初审FIX与逐项findings保留在review.md。单批修复后唯一一次增量复审PASS（review.md §7），F1–F4全部关闭。
- 独立复审执行四个真实loop测试各三轮、race通过，限定范围剩余Critical 0/Major 0/Minor 0；Leader已显式接受所有任务回执。
- Windows真机测试仍是后续验收，不冒充本轮通过；未自行提交。

## P4审核后的修复验证
- 审核结论FIX：3 Major/1 Minor，见review.md。F1–F3已组成单一后台修复任务，尚未完成。
- F4 Leader直接修复：EnvCheckSettings终态一次emit → EnvCheckView递增refreshRevision → WSLCLISettings watcher单次reload；无新轮询。
- F4 `npm run build`与`npm exec -- vitest run`（cwd=frontend）通过，仍41文件/413测试（f4-frontend-build.log、f4-frontend-tests.log）。
- F4浏览器：18项安装管理（新增跨兄弟卡片GetWSLCLIStatus调用与版本断言）+6项统一管理交互全部通过（browser-install-f4.log、browser-config-f4.log）。
- 用户文档补充联动触发条件与支持范围，纠正Pi已有凭据同步例外、停用不等于撤回授权的表述。
- F1–F3单批修复已合入（review-fix.md、review-fix.patch）：5分钟真实后台来源重扫、5秒延期补推心跳、每次对账幂等补授权；Disable/Shutdown同步停止循环，自然退出按done令牌清理槽。新增4条真实loop时序测试含直接换代与重启循环。
- 修复后Leader `go test ./...`、`go test -race . ./internal/wslsupply -run 'TestWSLUnified|TestWSLCliConfigAtLaunch|TestUnified|TestAutoConsent|TestExtendUnitSelection' -count=1`、`go vet ./...`、`GOOS=windows go build ./...` 全部exit 0（postfix-go-tests.log、postfix-race.log、postfix-vet.log、postfix-windows-build.log）。
- 修复未改变公开绑定签名，前端变动已在F4阶段build/413单测/24 browser通过；不需要再手改或生成绑定。

## 明确未覆盖
Windows/WSL真机：实际Linux npm安装/升级与进程超时清理、真实CLI消费配置/模型响应、网络与9P权限窗口。本轮宿主macOS；可移植runner测试、交叉编译和UI内存fixture均不能替代真机证据。
