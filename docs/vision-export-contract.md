# 契约 v1：amagi-media-understanding 视觉模型标记与导出

> 2026-05-16 | 父任务：通用型识图/识视频 skill + codebox 标记导出
> 三方共享契约：codebox 后端、codebox 前端、amagi-media-understanding skill。字段名与文件格式即 API，单方不得擅改。

## 1. TerminalPreset 新增字段（Go + 前端 TS 同步）

`internal/config/types.go` 的 `TerminalPreset` 增加：

```go
// 视觉能力标记：可作为识图 / 识视频模型导出
Vision         bool `json:"vision,omitempty"`
Video          bool `json:"video,omitempty"`
// 能力优先级，小者优先；0 视为 100
VisionPriority int  `json:"vision_priority,omitempty"`
```

- 持久化于 models.json `terminal_presets.*`，旧版本读新文件安全（未知字段忽略）。
- 标记独立于 preset 所在桶：anthropic 桶与 openai 桶的 preset 均可标记。

## 2. 导出文件 `~/.agents/amagi-media-models.json`

- 触发时机：`SaveTerminalPreset` / `DeleteTerminalPreset` / `SaveProvider` / `DeleteProvider` 成功后，以及启动配置加载完成后。
- 幂等全量重导出：无任何带标记 preset 时也写文件（`models: []`），便于区分「未配置」与「文件缺失」。写失败记 log 不阻断主流程。
- 文件权限 0600；目录 `~/.agents` 不存在则创建（0755）。
- 导出路径可被环境变量 `AMAGI_MEDIA_MODELS_PATH` 覆盖（测试用）。
- API key 来源：注入的 resolver（`SecretsService.GetAPIKey`），由 app.go 组装时 `SetAPIKeyResolver`；config 包不得直接依赖 secrets 包。

### JSON Schema（skill 消费端）

```json
{
  "version": 1,
  "updated_at": "RFC3339",
  "models": [
    {
      "id": "个人版API- Gemini/gemini-3.7-flash",
      "provider": "个人版API- Gemini",
      "preset": "gemini-3.7-flash",
      "model": "gemini-3.7-flash",
      "base_url": "http://api.maorun.top/v1",
      "api_key": "sk-...",
      "auth_key_env": "OPENAI_API_KEY",
      "api_type": "openai",
      "capabilities": ["image", "video"],
      "priority": 1,
      "parameters": { "reasoning_effort": "max", "max_tokens": 60000 }
    }
  ]
}
```

- `api_key`：resolver 拿到则写明文（文件已 0600）；拿不到写空串，skill fallback 读环境变量 `auth_key_env`（取 provider 的 `auth_key` 标识）。
- `base_url`：取 provider OpenAI 格式 base_url（`EffectiveBaseURLRaw("openai")`）。
- 跳过规则（v1 边界）：仅导出 OpenAI 兼容 provider（`type=openai`）；provider 仅有 anthropic 格式时该带标记 preset 跳过。视频视频标记 `video=true` 才进 `capabilities`。
- `parameters` 透传 `reasoning_effort` / `max_tokens` / `temperature` / `top_p`。
- `priority`：preset 的 `vision_priority`，0 归一化为 100。

## 3. skill `amagi-media-understanding`（marketplace `plugins/amagi-media-understanding/`）

```
.claude-plugin/plugin.json          # name: amagi-media-understanding, version 1.0.0
skills/amagi-media-understanding/SKILL.md        # 方法论（中英双语标题，正文中文，与 marketplace 现有风格一致）
skills/amagi-media-understanding/scripts/amagi-media-understanding.ts   # 零依赖，Node 22.6+ 原生 TS（与 codex-image-understanding 一致）
skills/amagi-media-understanding/agents/openai.yaml
skills/amagi-media-understanding/references/config-format.md  # 上述 JSON Schema 文档
```

### 行为契约

- 配置读取：默认 `~/.agents/amagi-media-models.json`，环境变量 `AMAGI_MEDIA_MODELS` 可覆盖路径。
- 模型选择：按 capability 过滤 → `priority` 升序 → 依次降级重试；用户 `--model <id或model名>` 指定时不降级。
- 请求：`POST {base_url}/chat/completions`；图片用 `image_url`（本地转 data URI，http(s) URL 原样）；视频用 `data:video/mp4;base64,...` 的 `input_audio` 同型 image_url data URI；headers `Authorization: Bearer <key>`。
- key 优先级：json `api_key` > env(`auth_key_env`)。
- luna 默认档位 max：透传 preset `parameters.reasoning_effort`（已为 max），Gemini flash 同理。
- 参数：`--prompt`/`--prompt-file` 二选一、`--mode image|video`（缺省按扩展名推断）、`--model`、`--timeout <s>`（默认 180，0 禁用）、`--` 分隔后接输入文件/URL，多张图一次调用。
- 失败处理：报真实错误（模型列表逐个尝试结果），禁止编造视觉内容。
- 视频限制：单文件 ≤8MB（与中转站常见限制一致），超限报错。

## 4. 验收

- Go：导出器单测（fake resolver）+ Save/Delete 联动 + `go vet ./...` + 全量 `go test ./internal/config ./internal/secrets`。
- 前端：`npm --prefix frontend run build`（含 vue-tsc）通过。
- 集成（Leader 统一执行）：给「个人版API- Gemini/gemini-3.7-flash」标记 image+video、「个人版API- GPT/gpt-5.6-luna」标记 image → 触发导出 → skill 真图真视频调用成功。

## 5. 边界（v1 不做）

- anthropic 格式 provider 的视觉导出（/v1/messages 协议）。
- 视频抽帧、音频抽取、字幕转写。
- 远程/移动端 API 暴露标记字段。
