package config

import (
	"regexp"
	"strings"
)

// 模型多模态能力自动发现（离线推断）。
//
// 背景：TerminalPreset 的 Vision/Video 手动标记是「覆盖项」而非主信息源——
// 用户不该为「模型是否支持图片输入」这种客观事实手工建档。本模块内置主流
// 模型族的能力知识库，从 model id 离线推断多模态理解（输入）能力，供两条
// 消费链自动补充：
//
//  1. launcher.ManagedPresetModels → pi/omp 托管模型条目的 input 字段
//     （缺失时下游默认 ["text"]，amagi-pi 守卫会误判多模态模型不支持图片，
//     拦截 read 图片直送——实战：amagi-kimi/k3 被误拦）。
//  2. buildVisionExportModel → ~/.agents/amagi-media-models.json（供
//     amagi-media-understanding skill 消费，契约 docs/vision-export-contract.md）。
//
// 原则：
//   - 保守收录：只收证据充分的模型族；漏报可由手动标记或扩充本表补救，
//     误报会把纯文本模型错标为多模态（下游 400 / skill 调错模型）。
//   - 优雅降级：消费端（skill）本身具备按 priority 降级重试，个别误报不致命。
//   - 仅推断「输入理解」能力（image/video in）。输出模态（图像/视频生成）
//     暂无消费方（pi models.json 无 output 字段、媒体导出 schema 仅理解向），
//     待有消费方时再扩展，避免死 schema。
//   - 只按 model id 匹配，不按 provider 名/baseURL 猜：provider 名是用户
//     自由文本（如「个人版API- Gemini」），命中模型族模式才是可靠信号。

// ModelModalities 模型的多模态理解（输入）能力。
type ModelModalities struct {
	Vision bool // 图片理解（image input）
	Video  bool // 视频理解（video input）
}

// modalityRule 单条模型族规则：pattern 匹配小写化、去 provider 前缀后的
// model id（如 "openai/gpt-4o" 以 "gpt-4o" 参与匹配）。
type modalityRule struct {
	pattern *regexp.Regexp
	mods    ModelModalities
	note    string // 收录依据（官方能力说明/实战验证），供审计与维护
}

func re(s string) *regexp.Regexp { return regexp.MustCompile(s) }

// modalityRules 有序规则表，先命中先生效。全部锚定 model id 前缀/族特征，
// 拒绝宽泛子串（防 "k3-plain" 这类 id 误命中 "k3"）。
var modalityRules = []modalityRule{
	// Anthropic：Claude 3 起全系视觉（官方：Claude 3/3.5/4 均支持图片输入）。
	{re(`^claude-(3|[4-9])(\.|-|$)`), ModelModalities{Vision: true}, "claude-3+ 全系 vision"},
	{re(`^claude-(sonnet|opus|haiku)-[4-9]`), ModelModalities{Vision: true}, "claude-*-4+ 命名系 vision"},

	// Google：Gemini 全系多模态，含视频理解（官方：1.5 起 image+audio+video）。
	{re(`^gemini-`), ModelModalities{Vision: true, Video: true}, "gemini 全系 vision+video"},
	{re(`^gemma-3-(4|12|27)b`), ModelModalities{Vision: true}, "gemma-3 4b/12b/27b 多模态（1b 为纯文本，不收）"},

	// OpenAI：4o/4.1/4.5/4-turbo/5.x/o 系均可图片输入（官方 vision 文档）；
	// gpt-image 系编辑接口接受图片输入。
	{re(`^gpt-4o`), ModelModalities{Vision: true}, "gpt-4o vision"},
	{re(`^gpt-4\.1`), ModelModalities{Vision: true}, "gpt-4.1 vision"},
	{re(`^gpt-4\.5`), ModelModalities{Vision: true}, "gpt-4.5 vision"},
	{re(`^gpt-4-turbo`), ModelModalities{Vision: true}, "gpt-4-turbo vision"},
	{re(`^gpt-5`), ModelModalities{Vision: true}, "gpt-5 系 vision"},
	{re(`^chatgpt-4o`), ModelModalities{Vision: true}, "chatgpt-4o vision"},
	{re(`^o[134](-|$)`), ModelModalities{Vision: true}, "o1/o3/o4 系 vision"},
	{re(`^gpt-image`), ModelModalities{Vision: true}, "gpt-image 编辑接受图片输入"},

	// Moonshot/Kimi：k3 系经实战确认（codebox kimi-coding provider 的 k3-256k
	// 条目本就声明 input=[text,image]，且主上确认 k3 支持图片输入）；
	// kimi-latest / moonshot-v1-*-vision / Kimi-VL 为官方视觉型号。
	{re(`^k3(-[0-9]+k)?$`), ModelModalities{Vision: true}, "kimi k3 系（实战验证）"},
	{re(`^kimi-latest`), ModelModalities{Vision: true}, "kimi-latest 官方 vision"},
	{re(`^moonshot-v1-[0-9]+k-vision`), ModelModalities{Vision: true}, "moonshot vision-preview 系"},
	{re(`^kimi-vl`), ModelModalities{Vision: true}, "Kimi-VL 系"},

	// 阿里 Qwen：VL 系与 QVQ 支持图片+视频理解；omni 系全模态。
	{re(`^qwen([0-9.]+)?-vl`), ModelModalities{Vision: true, Video: true}, "qwen-vl 系 vision+video"},
	{re(`^qvq`), ModelModalities{Vision: true, Video: true}, "qvq 视觉推理（含视频）"},
	{re(`^qwen([0-9.]+)?-omni`), ModelModalities{Vision: true, Video: true}, "qwen-omni 全模态"},

	// 智谱 GLM：glm-4v / glm-4.5v / glm-4.6v 视觉型号；cogvlm 开源视觉。
	{re(`^glm-4(\.[0-9]+)?v`), ModelModalities{Vision: true}, "glm-4v/4.5v/4.6v vision"},
	{re(`^cogvlm`), ModelModalities{Vision: true}, "cogvlm vision"},

	// Mistral：pixtral 视觉系；small-3.1+/medium-3 官方标注 vision。
	{re(`^pixtral`), ModelModalities{Vision: true}, "pixtral vision"},
	{re(`^mistral-(small-3\.[1-9]|medium-3)`), ModelModalities{Vision: true}, "mistral small3.1+/medium3 vision"},

	// Meta：llama-3.2-*-vision 与 llama-4（scout/maverick 原生多模态）。
	{re(`^llama-3\.2-.*vision`), ModelModalities{Vision: true}, "llama-3.2-vision"},
	{re(`^llama-4`), ModelModalities{Vision: true}, "llama-4 系多模态"},

	// DeepSeek：仅 VL 系视觉（v3/r1 为纯文本，不收）。
	{re(`^deepseek-vl`), ModelModalities{Vision: true}, "deepseek-vl"},

	// 字节豆包：vision 命名系支持图片理解。
	{re(`^doubao-.*vision`), ModelModalities{Vision: true}, "doubao vision 系"},
	{re(`^seed-.*vision`), ModelModalities{Vision: true}, "seed vision 系"},

	// 阶跃星辰：step-1v/1.5v 视觉型号；step-3 官方定位多模态推理。
	{re(`^step-1(\.5)?v`), ModelModalities{Vision: true}, "step-1v/1.5v vision"},
	{re(`^step-3(-|$)`), ModelModalities{Vision: true}, "step-3 多模态"},

	// xAI：grok-2-vision / grok-4 起支持图片输入。
	{re(`^grok-(2-vision|vision)`), ModelModalities{Vision: true}, "grok-2-vision"},
	{re(`^grok-4`), ModelModalities{Vision: true}, "grok-4+ vision"},

	// Amazon Nova：pro/lite/premier 多模态输入（图片+视频；micro 纯文本不收）。
	{re(`^amazon\.nova-(pro|lite|premier)`), ModelModalities{Vision: true, Video: true}, "nova pro/lite/premier vision+video"},
	{re(`^nova-(pro|lite|premier)`), ModelModalities{Vision: true, Video: true}, "nova 无前缀变体"},

	// 其他常见开源视觉族。
	{re(`^minimax-vl`), ModelModalities{Vision: true}, "minimax-vl"},
	{re(`^minicpm-v`), ModelModalities{Vision: true}, "minicpm-v"},
	{re(`^internvl`), ModelModalities{Vision: true}, "internvl 系"},
	{re(`^llava`), ModelModalities{Vision: true}, "llava 系"},
	{re(`^moondream`), ModelModalities{Vision: true}, "moondream"},
	{re(`^paligemma`), ModelModalities{Vision: true}, "paligemma"},
}

// LookupModelModalities 能力查询（三态）：返回 (能力, 是否已知)。
// 学习层精确命中（设备端实弹实证，含否定结论）优先于内置族规则；已知=true
// 时调用方不得再实弹探测（needsModalityProbeLocked 依赖此语义）。
func LookupModelModalities(providerName, modelID string) (ModelModalities, bool) {
	id := strings.ToLower(strings.TrimSpace(modelID))
	if id == "" {
		return ModelModalities{}, false
	}
	if idx := strings.LastIndex(id, "/"); idx >= 0 {
		id = id[idx+1:]
	}
	if mods, ok := lookupLearnedModalities(id); ok {
		return mods, true
	}
	for _, rule := range modalityRules {
		if rule.pattern.MatchString(id) {
			return rule.mods, true
		}
	}
	return ModelModalities{}, false
}

// InferModelModalities 离线推断模型的多模态理解能力。
//
// providerName 目前不参与判定（见文件头原则），保留在签名中以便未来引入
// provider 级规则时不改调用方。modelID 匹配前做小写化并剥离
// "provider/" 前缀（OpenRouter 风格）。未知模型返回零值——调用方据此
// 维持原有默认（不下发多模态声明），绝不猜测。
func InferModelModalities(providerName, modelID string) ModelModalities {
	mods, _ := LookupModelModalities(providerName, modelID)
	return mods
}

// AcceptsImageInput 该模型是否接受图片输入。Video 也计入——pi/omp 的 input
// 仅 text/image 两类，视频理解模型普遍接受图片帧输入，只标 video 的模型
// 若不下发 image input 同样会被下游守卫误拦。
func (m ModelModalities) AcceptsImageInput() bool {
	return m.Vision || m.Video
}
