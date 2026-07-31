# Pi 兼容性优化实施计划

> 依据：agent-outputs/pi-compat-gap-analysis.md（白泽摸底报告）
> 目标：将 Pi 支持补齐至 OpenCode/Codex 同等水平

## 已确认事实
- 启动/配置注入链路已对齐（buildPiCmd + models.json + PI_CODING_AGENT_DIR 隔离注入），不动。
- Pi 会话 JSONL（`~/.pi/agent/sessions` 或 `$PI_CODING_AGENT_DIR/sessions`）的 assistant 消息含 `usage{input,output,cacheRead,cacheWrite,cost}` + `provider/model/timestamp` → 用量同步可行，解除 T2 阻塞。

## 实施项

### Phase 1（并行，后端）
- **A. 插件/包管理后端**（luban）：新建 `internal/piplugin`（对标 opencodeplugin，封装 `pi install/list/remove/update`，读写 `~/.pi/agent/settings.json` packages[]）；app.go 装配 `PiPlugins`；附带 P1 增强：`BuildPiModelsConfig` 透传 compat/headers/authHeader。
- **B. 远程启动 + 用量统计**（luban）：`internal/remote` 加 `/api/sessions/launch-pi` + launchMeta Pi 分段；`internal/usage` 加 `appPi` 常量、dedup/billing 分支、pi 会话 JSONL 同步（注意 codebox 隔离目录 `$PI_CODING_AGENT_DIR/sessions` 与默认 `~/.pi/agent/sessions` 两处来源）。

### Phase 2（前端，依赖 A 的 API 形状）
- **C. 插件管理前端**（luoshen）：`api/piPlugin.ts` + `stores/piPlugin.ts` + `PiPluginPanel.vue` + ExtensionsView 加 Pi tab；顺带核验 UsageView 对 AppType="pi" 的展示兼容。

### Phase 3（验收）
- `go build ./...` + `go vet` + 前端 `vue-tsc` typecheck + wails build 冒烟；相关单测运行。

## 明确不做（避免范围蔓延）
- proxy/headroom：Claude 专属设计，Pi 不补。
- envcheck 版本兜底增强：现有 pi checker 与 codex 对等，够用。
- mobile 端启动 Pi 的 UI：后端端点先行，mobile UI 后续单独提。
