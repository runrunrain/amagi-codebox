# 契约 v1：amagi-media-understanding 视觉模型标记与导出

> 2026-05-16 | 父任务：通用型识图/识视频 skill + codebox 标记导出
> 三方共享契约：codebox 后端、codebox 前端、amagi-media-understanding skill。字段名与文件格式即 API，单方不得擅改。
> 状态：**已实施且经代码核对仍准确**（2026-08 复核：`internal/config/types.go` 字段与 tag、`internal/config/vision.go` 导出逻辑——优先级 0→100 归一化、anthropic-only 跳过、0600 原子写、`AMAGI_MEDIA_MODELS_PATH` 覆盖——均与本文一致）。
>
> **v1.1（2026-08-23）收录规则扩展**：手动 Vision/Video 标记由主信息源降级为**覆盖项**。新增 `internal/config/modalities.go` 内置主流模型族多模态能力知识库（`InferModelModalities`，按 model id 离线推断，保守收录）；导出收录改为「手动标记 ∪ 自动发现」——未手动标记但模型 id 命中已知多模态模型族的 preset 同样导出，capabilities 取并集。schema、优先级归一化、写盘语义均不变；同一推断也驱动 pi/omp 托管模型条目的 `input=["text","image"]` 声明（修 amagi-pi 守卫对 k3 等多模态模型的误判）。
>
> **v1.2（2026-08-23）实弹探测层**：静态知识库之外新增实证通道——预设/服务商保存与配置加载后，`ConfigService` 对「未手动标记、知识库未知、缓存未探」的模型调度异步探测（`ProbeModelModalities`：先读 `/models` 模态元数据，未决再发 1x1 PNG + max_tokens=1 实弹请求；仅 2xx/明确模态拒绝为有定论，网络/鉴权/限流/形态拒绝一律未决不落缓存）。有定论结论落 `AppConfig.ModalityProbe` 缓存（key `provider/model`，含否定结论防重复实弹），并重导出本文件 + 重同步 pi/omp 托管配置。能力判定三层并集：**手动标记 ∪ 探测缓存 ∪ 静态知识库**；探测否定结论不否决手动标记与 KB 已知能力。手动触发入口：`App.ProbeModelModalityNow`（Wails 绑定，前端预设弹窗「实弹探测」按钮），绕过已知性判定直接实证。
>
> **v1.3（2026-08-23）设备端学习层**：探测有定论结论同时回写 `~/.agents/amagi-modalities.json`（`ModalityKBFile`，key 为规范化模型 id，**跨 provider 泛化**，含否定结论；0600 原子写、`AMAGI_MODALITY_KB_PATH` 覆盖、缺失/损坏按空表自愈）。推断顺序：学习层精确命中（实证）优先于内置族规则（`LookupModelModalities` 三态：known=true 含否定，抑制重复实弹）。静态知识库 = 内置规则表（随二进制）+ 设备学习层（用户设备持久化，自学习增长）。
>
> **v1.4（2026-08-24）收录回归手动标记**：实战反馈——v1.1 的「手动 ∪ 自动发现」把用户从未标记视觉能力的预设（及 5 个 provider 的明文 key）全部写进导出文件，且 anthropic/openai 双桶同名预设各导一条（ID 重复），超出用户预期。修订：**导出收录与 capabilities 均仅取手动 Vision/Video 标记**（回归 v1.0 语义）；知识库/实弹探测/学习层继续存在，但仅驱动 pi/omp 托管条目 `input` 声明与前端「探测能力」提示，不再作为导出收录来源；探测落库不再触发视觉导出重写；新增跨桶同 ID 去重（openai 桶优先）。取代 v1.1/v1.2 中「导出收录三层并集」的描述，schema/优先级归一化/写盘语义不变。

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
- 跳过规则（v1 边界）：仅导出 OpenAI 兼容 provider（`type=openai`）；provider 仅有 anthropic 格式时该带标记 preset 跳过。视频标记 `video=true` 才进 `capabilities`。
- 收录规则（v1.4，取代 v1.1 三层并集）：**仅手动标记 Vision/Video 的 preset 导出**；`capabilities` 精确等于手动标记，不做知识库/探测并集。知识库与探测结论只驱动 pi/omp 托管条目 `input` 声明与前端探测提示。
- 跨桶去重（v1.4）：同一 provider/短名在 anthropic 与 openai 两桶同时标记时仅导出一条，openai 桶条目优先（与 `api_type=openai` 调用语义一致）。
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
