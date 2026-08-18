# Windows 内嵌 pi 不显示 TUI/Web 切换按钮 — 根因与修复

## 症状

Windows 设备启动内嵌终端 pi 会话（已装最新 amagi-pi 插件），TUI/Web 切换按钮不显示；macOS 正常。

## 根因

切换按钮要求 webui 探测达到 `available`（TerminalView `webUIToggleVisible`）。Windows 上探测永远失败：

1. **平台不对称**：Windows 内嵌 pi 走 `BootstrapShellAttach`（resolver.go：opencode/codex/pi 全用 attach）——ConPTY 只起 **shell**，`pi` 命令经 PTY 输入流注入 → codebox 注册的 tracker pid = **shell pid**；而 webui server 运行在 pi(node) 进程里，`/api/info` 返回 `info.PID` = **node pid**，必然失配。macOS darwin 分支直接 exec `pi`，pid 天然匹配（A-4 验证通过的原因）。
2. `validateAdoption` 严格 pid 等值 → 注入端口通道 200 也被拒；
3. 注册表回退入围要求 `pidMatch`（条目同样是 node pid）→ 全部条目跳过；
4. 两通道全败 → 45s 窗口耗尽 → `unavailable` → 按钮隐藏。

## 修复（token 即身份）

注入的 `AMAGI_WEBUI_TOKEN` 是壳与扩展的共享密钥；`/api/info` 受 capability 保护（错误 token → 401/403 → 不可达），**携带正确 token 的 200 已证明服务归属**，pid 校验在该场景冗余且在 shell-attach 架构下必然失配：

- `validateAdoption` 新增 `tokenProven`：token 非空且探测 200 时豁免 pid 等值（端口校验保留）；空 token（legacy/独立终端）维持 pid 防线防端口复用。
- 注册表回退入围新增 `tokenMatch`（注入 token == 条目 token）第三入围键，与 piSessionID 精确 / pidMatch 并列。
- 503 not-ready 分类的 pid 校验维持不变（任意服务可返回 503 且不校验 Authorization，pid 是该窗口唯一区分手段；窗口期失配只损失一轮探测，下一轮 200 即恢复）。

## 测试（internal/webui/webui_test.go）

- `TestProbe_WindowsShellAttachTokenProvenAdopted`：shell pid 4321 / node pid 9999 + token → 采纳 available ✅
- `TestProbe_WindowsShellAttachEmptyTokenStillRejected`：空 token + pid 失配 → 仍拒绝 ✅
- 既有用例（含 503 错 pid 转回退、resume 会话切换、粘性校验）零回归；`go test -race` 通过。

## 验证

`go vet ./...` 净；`go test ./internal/webui/ -count=1` + `-race` 全绿。

Windows 手验：启动内嵌 pi → 数秒内出现 TUI/Web 切换按钮 → 切 Web 平面正常显示。
