# 交付总结：webui 远程输入「invalid protocol response」根因根治

- 日期：2026-09-20
- Leader：天城（taibai 会话）
- 范围：amagi-codebox（本仓）+ amagi-pi（/Users/maorun/maorun-workpace/amagi-pi）
- 状态：三路修复完成，diting 集成审核 PASS_WITH_MINOR（3 Minor 已处置），四任务全部 accepted
- 前置根因报告：本会话对话记录（实测证据链：读面 200/101 正常、写面 403 control.forbidden、错误体无 v:1）

## 根因（简述）

远程设备在 pi Web 会话视图输入 → `POST /api/input` 被 CodeBox 写面控制门 403（设备未持控制权）→ 代理错误体为 v1 REST 契约形状（无 `v:1`）→ amagi-pi webui 前端 `sendInput` 硬校验 `json.v === 1` → `failProtocol`（断 WS、永久 degraded）→ 显示 "invalid protocol response" +「连接中断 · 重连中」，真实错误码被完全掩盖；且移动端 webplane 视图无控制权获取入口（UX 断层）。

## 改动清单

### amagi-codebox（未提交，工作树）
| 文件 | 改动 | 任务 |
| --- | --- | --- |
| `internal/remote/webui_proxy.go` | `writeWebUIPlaneError`（v1 字段上注入 `v:1`+`error:<code>`）+ `webUIProxyEnforceAuth` 本地鉴权镜像；6 处 iframe 数据面错误点切换 | T1 (luban) |
| `internal/remote/webui_proxy_test.go` | `TestWebUIProxy_ErrorEnvelope`（五类信封断言）+ `TestWebUIProxy_AuthMirrorEnvelopes`（四分支 parity 锁，diting F-2/F-3 补强，Leader 直修）+ v1 面防污染回归 | T1 + Leader |
| `mobile/src/views/WorkspacePage.vue` | webplane 条件化控制权提示条 + 「接管控制」按钮（显式 `store.acquire()`，无自动抢占） | T3 (luban) |
| `mobile/src/__tests__/views/WorkspacePage.webplane.test.ts` | 9 个新用例（条件/接管/消失/反例） | T3 (luban) |

### amagi-pi（未提交，工作树）
| 文件 | 改动 | 任务 |
| --- | --- | --- |
| `webui/src/http-source.ts` | `sendInput`/`probeReady`/history 错误信封分流（≥4xx 且 error/code 短码 → 请求级错误，不 failProtocol；2xx 缺 v 才 failProtocol；410 cursor_expired 专用路径保留） | T2 (luban) |
| `webui/src/controller.ts` | 错误码 `error ?? code` 兼容 + `control.forbidden`/`auth.unpaired`/`service.down`/`session.not_found` 文案映射 | T2 |
| `webui/src/protocol.ts` | `InputErrorResponse` 类型放宽（error 可选、新增 `code?`）——**F-1 补记：T2 任务书声明清单遗漏此文件，属必要类型配套，非越权改动** | T2 |
| `extensions/amagi-core/webui/server.ts` | `jsonResponse` 契约冻结注释（经 T2 实测核实：自始统一注入 v:1，含 4xx/5xx——任务书原假设「后端错误无 v」与 HEAD 不符，已修正记录） | T2 |
| `docs/webui-protocol.md` | v1.0.16 补注：错误响应携带 v:1、客户端不得对错误信封触发协议违规判定（§1/§4.4/§9/§13 G3） | T2 |
| `webui/tests/input-error-protocol.test.ts` | 新增 7 例（403 反代信封不断 WS、409 steer 重发可达、200 无 v 仍 failProtocol、映射与优先级） | T2 |
| `tests/webui-server.test.mjs` | 后端 4xx/5xx 带 v:1 回归锁定（405/503/400/409/403/404） | T2 |
| `extensions/amagi-core/webui/static/` | 构建产物刷新（index-ZA8FG_mR.js → index-BdZFQkp8.js，新 bundle 含修复串） | T2 |

## diting 集成审核（full 档）

报告：`diting-integration-review.md`（同目录）。VERDICT: **PASS_WITH_MINOR**（0 Critical / 0 Major）。

- 跨仓协议逐字对齐（注入码值 ↔ 映射四码一致）；新旧前端 × 新旧代理四组合推演不劣化
- 安全红线全过：错误体无 token 泄漏、ACAO:null 保持、v1 REST 面冻结有测试锁、2xx 不误放行
- Minor 处置：F-1 本记录补记；F-2/F-3 Leader 直修（ErrorHandler ACAO:null 真断言 + 镜像鉴权四分支表驱动 parity 锁）

## 验证矩阵（Leader 独立复核）

| 验证 | 范围 | 结果 |
| --- | --- | --- |
| `go vet ./...` + `go test ./internal/remote -count=1`（含 browser e2e） | amagi-codebox | ✅ 全绿（26s） |
| `npm run typecheck` / `npm run depcruise` | amagi-pi | ✅ 0 违规 |
| `npm test` | amagi-pi | ✅ 2755 pass / 0 fail |
| `npm --prefix webui test` | amagi-pi/webui | ✅ 223 pass（含新增 7 例） |
| mobile vitest（651 用例）+ vue-tsc | amagi-codebox/mobile | ✅ 全绿 |

## 生效条件与后续

- 两仓均**未提交**（主上裁定提交时机与版本号：codebox wails.json 惯例 fix+release；amagi-pi 2.6.x）
- 桌面 CodeBox 需重新构建部署后新代理信封生效；pi 会话重启后加载新 amagi-pi 扩展与 webui 产物
- 真机建议：手机端打开 pi 会话 Web 平面 → 未持控制权时将见提示条+接管按钮；接管后输入台可写入；若仍被拒将显示真实错误码文案（如「需要会话控制权…」）而非 invalid protocol response
