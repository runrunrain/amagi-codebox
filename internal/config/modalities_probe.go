package config

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// 多模态能力实弹探测（契约 docs/vision-export-contract.md v1.2）。
//
// 静态知识库（modalities.go）是离线兜底；对知识库未知的模型，预设/服务商
// 保存后由 ConfigService 调度本模块对 OpenAI 兼容端点做实证探测，结果经
// RecordModalityProbe 落入 AppConfig.ModalityProbe 缓存（仅记有定论的结果）。
//
// 探测策略（按成本升序，先命中先返回）：
//  1. GET {base}/models：部分网关（OpenRouter 等）在模型清单中携带
//     architecture.input_modalities 等模态元数据——零生成成本，优先尝试；
//     条目存在但无模态字段视为元数据不可用，继续实弹探测。
//  2. POST {base}/chat/completions 实弹图片探测：1x1 PNG data URI +
//     max_tokens=1。2xx → 确认 vision；400/422 且响应体命中模态拒绝关键词 →
//     确认无 vision；其余（401/403/404/429/5xx/网络/超时）一律未决——
//     未决结果不落缓存（下次保存/启动自然重试），绝不把环境故障误记为能力不足。
//
// 视频能力不做实弹探测（载荷重、计费不划算），仅采信 /models 元数据与静态
// 知识库。本包不依赖 secrets：API key 由调用方（app 组装层）解析后传入。

const (
	// ModalityProbeSourceModelsAPI 能力结论来自 /models 元数据。
	ModalityProbeSourceModelsAPI = "models-api"
	// ModalityProbeSourceImageProbe 能力结论来自实弹图片请求。
	ModalityProbeSourceImageProbe = "image-probe"
)

// modalityProbeMaxBodyBytes 限制响应读取体积（错误体可能很长，仅需头部片段
// 做关键词分类）。
const modalityProbeMaxBodyBytes = 8192

// onePixelPNGDataURI 1x1 透明 PNG 的 data URI（实弹探测的最小图片载荷）。
const onePixelPNGDataURI = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

// modalityRejectionKeywords 模态拒绝响应体的关键词（小写匹配）。命中即判定
// 「模型明确不接受图片输入」；未命中的 4xx 视为参数/网关问题，不下结论。
var modalityRejectionKeywords = []string{
	"image", "multimodal", "vision", "modalit",
	"does not support", "unsupported content", "invalid content",
}

// ProbeModelModalities 对单个模型做实弹多模态能力探测。
//
// 返回 (能力, 来源, 是否有定论)。baseURL 应为规范化后的 OpenAI 兼容基址
// （EffectiveBaseURL("openai")）；apiKey 允许为空（部分内网网关免鉴权）。
// client 由调用方提供（超时/代理策略归调用方管理）。
func ProbeModelModalities(ctx context.Context, client *http.Client, baseURL, apiKey, model string) (ModelModalities, string, bool) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	model = strings.TrimSpace(model)
	if client == nil || base == "" || model == "" {
		return ModelModalities{}, "", false
	}
	if mods, ok := probeViaModelsAPI(ctx, client, base, apiKey, model); ok {
		return mods, ModalityProbeSourceModelsAPI, true
	}
	if mods, ok := probeViaImageRequest(ctx, client, base, apiKey, model); ok {
		return mods, ModalityProbeSourceImageProbe, true
	}
	return ModelModalities{}, "", false
}

// probeViaModelsAPI 从 GET {base}/models 的模型条目提取模态元数据。
// 仅当目标模型条目存在且携带可识别的模态字段时才有定论；否则 ok=false。
func probeViaModelsAPI(ctx context.Context, client *http.Client, base, apiKey, model string) (ModelModalities, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return ModelModalities{}, false
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return ModelModalities{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ModelModalities{}, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, modalityProbeMaxBodyBytes*32))
	if err != nil {
		return ModelModalities{}, false
	}
	var list struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return ModelModalities{}, false
	}
	for _, entry := range list.Data {
		id, _ := entry["id"].(string)
		if !strings.EqualFold(id, model) {
			continue
		}
		mods, recognized := parseModalityMetadata(entry)
		if recognized {
			return mods, true
		}
		// 条目存在但无模态元数据：/models 路径无法定论，交由实弹探测。
		return ModelModalities{}, false
	}
	return ModelModalities{}, false
}

// parseModalityMetadata 识别若干常见的模态元数据形态：
// OpenRouter 的 architecture.input_modalities、平铺的 input_modalities、
// 以及 modalities: "text+image->text" 字符串（OpenRouter 旧形态）。
func parseModalityMetadata(entry map[string]any) (ModelModalities, bool) {
	if arch, ok := entry["architecture"].(map[string]any); ok {
		if mods, ok := modalitiesFromList(arch["input_modalities"]); ok {
			return mods, true
		}
	}
	if mods, ok := modalitiesFromList(entry["input_modalities"]); ok {
		return mods, true
	}
	if s, ok := entry["modality"].(string); ok {
		return modalitiesFromArrowString(s), true
	}
	if s, ok := entry["modalities"].(string); ok {
		return modalitiesFromArrowString(s), true
	}
	return ModelModalities{}, false
}

// modalitiesFromList 从 ["text","image","video"] 形态的数组解析输入模态。
func modalitiesFromList(v any) (ModelModalities, bool) {
	items, ok := v.([]any)
	if !ok || len(items) == 0 {
		return ModelModalities{}, false
	}
	var mods ModelModalities
	for _, item := range items {
		s, _ := item.(string)
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "image":
			mods.Vision = true
		case "video":
			mods.Video = true
		}
	}
	return mods, true
}

// modalitiesFromArrowString 解析 "text+image->text" 形态（箭头左侧为输入）。
func modalitiesFromArrowString(s string) ModelModalities {
	input := strings.ToLower(s)
	if idx := strings.Index(input, "->"); idx >= 0 {
		input = input[:idx]
	}
	return ModelModalities{
		Vision: strings.Contains(input, "image"),
		Video:  strings.Contains(input, "video"),
	}
}

// probeViaImageRequest 实弹图片探测：发送 1x1 PNG + max_tokens=1。
// 2xx → vision=true 定论；400/422 且响应体命中模态拒绝关键词 → vision=false
// 定论；其余一律未决。
func probeViaImageRequest(ctx context.Context, client *http.Client, base, apiKey, model string) (ModelModalities, bool) {
	payload := map[string]any{
		"model":      model,
		"max_tokens": 1,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": "Reply with: ok"},
					{"type": "image_url", "image_url": map[string]string{"url": onePixelPNGDataURI}},
				},
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ModelModalities{}, false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ModelModalities{}, false
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return ModelModalities{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return ModelModalities{Vision: true}, true
	}
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusUnprocessableEntity {
		return ModelModalities{}, false
	}
	errBody, err := io.ReadAll(io.LimitReader(resp.Body, modalityProbeMaxBodyBytes))
	if err != nil {
		return ModelModalities{}, false
	}
	lower := strings.ToLower(string(errBody))
	// 形态拒绝≠能力拒绝：网关不接受 data URI/无法拉取图片 URL 时，报文虽含
	// "image" 字样，但模型本身的视觉能力未被证伪，按未决处理。
	for _, kw := range []string{"data uri", "data:image", "invalid url", "fetch"} {
		if strings.Contains(lower, kw) {
			return ModelModalities{}, false
		}
	}
	for _, kw := range modalityRejectionKeywords {
		if strings.Contains(lower, kw) {
			// 明确的模态拒绝：模型不接受图片输入（定论的否定结果）。
			return ModelModalities{}, true
		}
	}
	return ModelModalities{}, false
}

// ModalityProbeSnapshot 探测缓存的只读快照类型（key 为 provider/model）。
// 快照而非闭包的原因：ConfigService 是 Wails 绑定服务，函数类型返回值无法
// 生成 TS 模型；快照可序列化，下游（provider_sync/launch_planner）在同步
// 时刻取一份即可——探测结论落盘后会重跑同步，新一轮自然拿到最新快照。
type ModalityProbeSnapshot = map[string]ModalityProbeEntry

// LookupProbedSafe nil/缺键安全的快照查询。
func LookupProbedSafe(snapshot ModalityProbeSnapshot, provider, model string) ModelModalities {
	if len(snapshot) == 0 {
		return ModelModalities{}
	}
	entry, ok := snapshot[ModalityProbeKey(provider, model)]
	if !ok {
		return ModelModalities{}
	}
	return ModelModalities{Vision: entry.Vision, Video: entry.Video}
}

// String 探测结论调试输出。
func (e ModalityProbeEntry) String() string {
	return fmt.Sprintf("vision=%v video=%v source=%s at=%s", e.Vision, e.Video, e.Source, e.ProbedAt)
}
