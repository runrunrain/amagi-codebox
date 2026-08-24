package config

import "testing"

// 能力推断表测试：每个收录模型族至少一个正向用例 + 易混淆负向用例成对，
// 防宽泛模式误伤纯文本模型（误报会把文本模型错标多模态 → 下游 400/调错模型）。
func TestInferModelModalities(t *testing.T) {
	cases := []struct {
		model string
		want  ModelModalities
	}{
		// Kimi/Moonshot：实战验证族（k3）+ 官方视觉型号。
		{"k3", ModelModalities{Vision: true}},
		{"k3-256k", ModelModalities{Vision: true}},
		{"K3-256K", ModelModalities{Vision: true}}, // 大小写不敏感
		{"kimi-latest", ModelModalities{Vision: true}},
		{"moonshot-v1-8k-vision-preview", ModelModalities{Vision: true}},
		{"kimi-vl-a3b-thinking", ModelModalities{Vision: true}},

		// Anthropic / Google / OpenAI 主流多模态。
		{"claude-3-5-sonnet-20241022", ModelModalities{Vision: true}},
		{"claude-sonnet-4-5", ModelModalities{Vision: true}},
		{"gemini-3.7-flash", ModelModalities{Vision: true, Video: true}},
		{"gemini-2.5-pro", ModelModalities{Vision: true, Video: true}},
		{"gpt-4o", ModelModalities{Vision: true}},
		{"gpt-5.6-luna", ModelModalities{Vision: true}},
		{"o4-mini", ModelModalities{Vision: true}},
		{"openai/gpt-4o", ModelModalities{Vision: true}}, // OpenRouter 风格前缀剥离

		// Qwen / GLM / 其他视觉族。
		{"qwen2.5-vl-72b-instruct", ModelModalities{Vision: true, Video: true}},
		{"qvq-max", ModelModalities{Vision: true, Video: true}},
		{"qwen3-omni-flash", ModelModalities{Vision: true, Video: true}},
		{"glm-4.5v", ModelModalities{Vision: true}},
		{"glm-4v-plus", ModelModalities{Vision: true}},
		{"pixtral-large", ModelModalities{Vision: true}},
		{"llama-3.2-11b-vision-instruct", ModelModalities{Vision: true}},
		{"doubao-1.5-vision-pro-32k", ModelModalities{Vision: true}},
		{"step-1v-8k", ModelModalities{Vision: true}},
		{"grok-4-fast", ModelModalities{Vision: true}},
		{"minicpm-v-2_6", ModelModalities{Vision: true}},
		{"internvl3-8b", ModelModalities{Vision: true}},

		// 负向：纯文本模型不得误标（保守原则的验收）。
		{"deepseek-v3", ModelModalities{}},
		{"deepseek-r1", ModelModalities{}},
		{"qwen3-32b", ModelModalities{}},      // 非 VL 的 qwen 纯文本
		{"glm-5.3", ModelModalities{}},        // 无 v 后缀的 glm 纯文本
		{"gpt-3.5-turbo", ModelModalities{}},  // 老模型无 vision
		{"claude-2.1", ModelModalities{}},     // claude-2 系无 vision
		{"gemma-3-1b-it", ModelModalities{}},  // gemma-3 1b 纯文本
		{"k3-plain", ModelModalities{}},       // 防 "k3" 前缀宽泛误命中
		{"fallback-model", ModelModalities{}}, // 测试常用占位 id
		{"acme-text-9", ModelModalities{}},    // 未知族
		{"kimi-k2", ModelModalities{}},        // k2 系文本（未确认不收）
		{"cogview-3-plus", ModelModalities{}}, // 图像「生成」模型不算理解
		{"", ModelModalities{}},               // 空 id
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			if got := InferModelModalities("any-provider", tc.model); got != tc.want {
				t.Errorf("InferModelModalities(%q) = %+v, want %+v", tc.model, got, tc.want)
			}
		})
	}
}

// AcceptsImageInput 语义：vision 或 video 任一成立即接受图片输入（video 模型
// 普遍接受图片帧）；皆空则否。
func TestModelModalitiesAcceptsImageInput(t *testing.T) {
	if !(ModelModalities{Vision: true}).AcceptsImageInput() {
		t.Error("vision-only should accept image input")
	}
	if !(ModelModalities{Video: true}).AcceptsImageInput() {
		t.Error("video-only should accept image input (image frames)")
	}
	if (ModelModalities{}).AcceptsImageInput() {
		t.Error("empty modalities must not accept image input")
	}
}
