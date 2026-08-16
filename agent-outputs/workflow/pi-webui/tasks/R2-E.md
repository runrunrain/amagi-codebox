# 任务包 R2-E：M1 修复后 E2E 复验（token 链路 + 安全复验）

- 双现场：codebox `/Users/maorun/maorun-workpace/amagi-codebox/.amagi-worktrees/webui15`；amagi-pi `/Users/maorun/maorun-workpace/amagi-pi/.amagi-worktrees/webui11`（已由 Leader 重建自分支 amagi/webui11@4b4bcde，npm test 1029/1029 复活验证过）
- 背景：R1 修复了 diting 审出的 1C+3M+3m（契约 v1.0.2 capability 鉴权、探测强校验、分页边界、生命周期）。单测全绿，本任务做真实链路复验。

## 复验清单（wails dev 真实 app，settings.json 受控例外同 I-1 手法：备份→指向 webui11→还原+diff 证据）

1. **A-1/A-2 复验**（token 链路下）：切换控件出现（探测 Bearer 200）→ TUI↔Web 切换 → Web 历史+实时流式（iframe fragment→Bearer/WS 子协议全链路）→ 切回 TUI 存活。
2. **安全复验**（新增证据，对应 Critical 修复）：
   - `curl -H "Origin: https://attacker.example" http://127.0.0.1:<port>/api/history` → 403；
   - 无 Authorization → 403；错 token → 403；
   - WS 错 token 握手 → 拒绝（非 101）；
   - 静态资源仍 200（公开设计）；页面 console 无错误。
3. **A-6 直连复验**（token 回退链）：独立终端跑 pi（无 env token）→ 注册表条目含 token（0600）→ 手动拼 `#/t=<token>` 浏览器直连成功。
4. **A-4 抽查**：还原 settings 后新 pi 会话切换控件不出现（R1 未改路径，抽查即可）。
5. **回归**：codebox `go vet ./... && go test ./... -count=1` + `npm --prefix frontend run build`（顺带 wails build 再生 wailsjs 签名，R1-B 遗留）；amagi-pi 侧 Leader 已验 1029/1029，如你在 webui11 有改动才需重跑。
6. **证据**：截图/日志落 `调研报告/spike-assets/r2-*.png`；在 `i1-验收记录.md` 追加「R2 复验」节（逐项结果+安全复验输出原文）。

## 纪律
小 bug 直接修（worktree 内）+记录；语义级登记不硬改；返回逐项结论+证据路径+修复清单。
