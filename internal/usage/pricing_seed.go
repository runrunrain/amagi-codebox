package usage

import "time"

// defaultPricingData 返回内置价格表 seed（设计附录 B）。
//
// 价格单位：micro-native-currency per million tokens。
//   - USD 模型：3.00 USD/M → 3_000_000 micro-USD/M
//   - CNY 模型：1.0 CNY/M  → 1_000_000 micro-CNY/M
//
// 所有价格为公开参考值，可能与实际供应商账单有差异；用户可在前端编辑。
// 未知模型走 zero_cost 兜底（token 照记，cost 置 0）。
func defaultPricingData() *PricingData {
	now := time.Now().UTC()
	models := []ModelPricing{}

	// add 添加一条 seed；调用方保证 ModelPattern 已标准化（小写、无 vendor 前缀）。
	add := func(pattern, display, provider, currency string,
		in, out, cr, cc int64, notes string) {
		models = append(models, ModelPricing{
			ID:                      "builtin-" + pattern,
			ModelPattern:            pattern,
			DisplayName:             display,
			Provider:                provider,
			CurrencyCode:            currency,
			InputPerMillion:         in,
			OutputPerMillion:        out,
			CacheReadPerMillion:     cr,
			CacheCreationPerMillion: cc,
			IsBuiltin:               true,
			Notes:                   notes,
			UpdatedAt:               now,
		})
	}

	// === Claude 系（Anthropic，USD） ===
	// 官方价（USD/M tokens）。
	add("claude-sonnet-4", "Claude Sonnet 4", "anthropic", "USD",
		3_000_000, 15_000_000, 300_000, 3_750_000, "Anthropic 2025 参考价")
	add("claude-sonnet-4-20250514", "Claude Sonnet 4 (2025-05-14)", "anthropic", "USD",
		3_000_000, 15_000_000, 300_000, 3_750_000, "Anthropic 2025 参考价")
	add("claude-opus-4", "Claude Opus 4", "anthropic", "USD",
		15_000_000, 75_000_000, 1_500_000, 18_750_000, "Anthropic 2025 参考价")
	add("claude-opus-4-20250514", "Claude Opus 4 (2025-05-14)", "anthropic", "USD",
		15_000_000, 75_000_000, 1_500_000, 18_750_000, "Anthropic 2025 参考价")
	add("claude-3-5-sonnet", "Claude 3.5 Sonnet", "anthropic", "USD",
		3_000_000, 15_000_000, 300_000, 3_750_000, "Anthropic 参考价")
	add("claude-3-5-sonnet:20241022", "Claude 3.5 Sonnet (2024-10-22)", "anthropic", "USD",
		3_000_000, 15_000_000, 300_000, 3_750_000, "Anthropic 参考价")
	add("claude-3-5-haiku-20241022", "Claude 3.5 Haiku", "anthropic", "USD",
		800_000, 4_000_000, 80_000, 1_000_000, "Anthropic 参考价")
	add("claude-3-opus", "Claude 3 Opus", "anthropic", "USD",
		15_000_000, 75_000_000, 1_500_000, 18_750_000, "Anthropic 参考价（已下线）")
	add("claude-3-haiku", "Claude 3 Haiku", "anthropic", "USD",
		250_000, 1_250_000, 25_000, 300_000, "Anthropic 参考价")

	// === GPT 系（OpenAI，USD） ===
	// 含 cache 扣减语义（codex 路径）；价格按公开参考。
	add("gpt-5", "GPT-5", "openai", "USD",
		1_250_000, 10_000_000, 125_000, 0, "OpenAI 参考价")
	add("gpt-5-mini", "GPT-5 mini", "openai", "USD",
		250_000, 2_000_000, 25_000, 0, "OpenAI 参考价")

	// GPT-5.x 系列（OpenAI 官方短上下文标准价，参考值）。
	// 主上 codex 实测在用 gpt-5.6-sol；其余为同家族参考价。
	// cacheCreation 为 0 的模型官方未公布 prompt cache creation 计费，按 0 入库。
	add("gpt-5.6-sol", "GPT-5.6 Sol", "openai", "USD",
		5_000_000, 30_000_000, 500_000, 6_250_000,
		"OpenAI 官方短上下文标准价，参考价")
	add("gpt-5.6-terra", "GPT-5.6 Terra", "openai", "USD",
		2_500_000, 15_000_000, 250_000, 3_125_000,
		"OpenAI 官方短上下文标准价，参考价")
	add("gpt-5.6-luna", "GPT-5.6 Luna", "openai", "USD",
		1_000_000, 6_000_000, 100_000, 1_250_000,
		"OpenAI 官方短上下文标准价，参考价")
	add("gpt-5.5", "GPT-5.5", "openai", "USD",
		5_000_000, 30_000_000, 500_000, 0,
		"OpenAI 官方短上下文标准价，参考价")
	add("gpt-5.3-codex", "GPT-5.3 Codex", "openai", "USD",
		1_750_000, 14_000_000, 175_000, 0,
		"OpenAI 官方短上下文标准价，参考价；codex 专用")
	add("gpt-4o", "GPT-4o", "openai", "USD",
		2_500_000, 10_000_000, 1_250_000, 0, "OpenAI 参考价")
	add("gpt-4o-2024-08-06", "GPT-4o (2024-08-06)", "openai", "USD",
		2_500_000, 10_000_000, 1_250_000, 0, "OpenAI 参考价")
	add("gpt-4o-mini", "GPT-4o mini", "openai", "USD",
		150_000, 600_000, 75_000, 0, "OpenAI 参考价")
	add("gpt-4-turbo", "GPT-4 Turbo", "openai", "USD",
		10_000_000, 30_000_000, 0, 0, "OpenAI 参考价")
	add("gpt-4", "GPT-4", "openai", "USD",
		30_000_000, 60_000_000, 0, 0, "OpenAI 参考价")
	add("gpt-4.1", "GPT-4.1", "openai", "USD",
		2_000_000, 8_000_000, 500_000, 0, "OpenAI 参考价")
	add("gpt-4.1-mini", "GPT-4.1 mini", "openai", "USD",
		400_000, 1_600_000, 100_000, 0, "OpenAI 参考价")
	add("o1", "OpenAI o1", "openai", "USD",
		15_000_000, 60_000_000, 7_500_000, 0, "OpenAI 参考价；含 reasoning")
	add("o1-preview", "OpenAI o1 preview", "openai", "USD",
		15_000_000, 60_000_000, 7_500_000, 0, "OpenAI 参考价；含 reasoning")
	add("o1-mini", "OpenAI o1 mini", "openai", "USD",
		3_000_000, 12_000_000, 1_500_000, 0, "OpenAI 参考价；含 reasoning")
	add("o3", "OpenAI o3", "openai", "USD",
		10_000_000, 40_000_000, 5_000_000, 0, "OpenAI 参考价；含 reasoning")
	add("o3-mini", "OpenAI o3 mini", "openai", "USD",
		3_000_000, 12_000_000, 1_500_000, 0, "OpenAI 参考价；含 reasoning")

	// === GLM 系（智谱，CNY） ===
	// 国产模型按 CNY 计价（设计 6.5）。
	add("glm-5", "GLM-5", "glm", "CNY",
		2_000_000, 8_000_000, 200_000, 0, "智谱参考价")
	add("glm-5-turbo", "GLM-5 Turbo", "glm", "CNY",
		500_000, 500_000, 50_000, 0, "智谱参考价")
	add("glm-5.2", "GLM-5.2", "glm", "CNY",
		2_000_000, 8_000_000, 200_000, 0, "智谱参考价")
	add("glm-4.6", "GLM-4.6", "glm", "CNY",
		2_000_000, 8_000_000, 200_000, 0, "智谱参考价")
	add("glm-4.6-air", "GLM-4.6 Air", "glm", "CNY",
		500_000, 500_000, 50_000, 0, "智谱参考价")
	add("glm-4.5", "GLM-4.5", "glm", "CNY",
		2_000_000, 8_000_000, 200_000, 0, "智谱参考价")
	add("glm-4.5-air", "GLM-4.5 Air", "glm", "CNY",
		500_000, 500_000, 50_000, 0, "智谱参考价")
	add("glm-4-plus", "GLM-4 Plus", "glm", "CNY",
		50_000_000, 50_000_000, 0, 0, "智谱参考价")
	add("glm-4-flash", "GLM-4 Flash", "glm", "CNY",
		0, 0, 0, 0, "智谱免费模型")
	add("glm-4-long", "GLM-4 Long", "glm", "CNY",
		1_000_000, 1_000_000, 0, 0, "智谱参考价")
	add("glm-4v", "GLM-4V", "glm", "CNY",
		50_000_000, 50_000_000, 0, 0, "智谱视觉模型参考价")
	add("glm-4v-plus", "GLM-4V Plus", "glm", "CNY",
		10_000_000, 10_000_000, 0, 0, "智谱视觉模型参考价")

	// === DeepSeek（深度求索，CNY） ===
	add("deepseek-chat", "DeepSeek Chat", "deepseek", "CNY",
		2_000_000, 8_000_000, 200_000, 0, "DeepSeek 参考价；对应 V3")
	add("deepseek-v3", "DeepSeek V3", "deepseek", "CNY",
		2_000_000, 8_000_000, 200_000, 0, "DeepSeek 参考价")
	add("deepseek-reasoner", "DeepSeek Reasoner", "deepseek", "CNY",
		4_000_000, 16_000_000, 400_000, 0, "DeepSeek 参考价；含 reasoning")
	add("deepseek-r1", "DeepSeek R1", "deepseek", "CNY",
		4_000_000, 16_000_000, 400_000, 0, "DeepSeek 参考价；含 reasoning")
	add("deepseek-coder", "DeepSeek Coder", "deepseek", "CNY",
		1_000_000, 2_000_000, 100_000, 0, "DeepSeek 参考价")
	// DeepSeek-V4-Pro official API pricing (2026-07): cache miss $0.435/m,
	// cache hit $0.003625/m, output $0.87/m. The cache-hit line is kept
	// separate so cache economics and actual estimated cost stay accurate.
	add("deepseek-v4-pro", "DeepSeek V4 Pro", "deepseek", "USD",
		435_000, 870_000, 3_625, 0, "DeepSeek API 官方价（2026-07，缓存命中已折扣）")

	// === Kimi / Moonshot（月之暗面，CNY） ===
	add("moonshot-v1-8k", "Moonshot v1 8K", "moonshot", "CNY",
		12_000_000, 12_000_000, 0, 0, "Moonshot 参考价")
	add("moonshot-v1-32k", "Moonshot v1 32K", "moonshot", "CNY",
		24_000_000, 24_000_000, 0, 0, "Moonshot 参考价")
	add("moonshot-v1-128k", "Moonshot v1 128K", "moonshot", "CNY",
		60_000_000, 60_000_000, 0, 0, "Moonshot 参考价")
	add("kimi-k2", "Kimi K2", "moonshot", "CNY",
		4_000_000, 16_000_000, 0, 0, "Moonshot 参考价")

	// === MiniMax（CNY） ===
	add("abab6.5-chat", "ABAB 6.5 Chat", "minimax", "CNY",
		30_000_000, 30_000_000, 0, 0, "MiniMax 参考价")
	add("minimax-text-01", "MiniMax Text 01", "minimax", "CNY",
		2_000_000, 2_000_000, 0, 0, "MiniMax 参考价")

	// === Doubao（字节跳动，CNY） ===
	add("doubao-pro-32k", "Doubao Pro 32K", "doubao", "CNY",
		800_000, 2_000_000, 0, 0, "字节参考价")
	add("doubao-pro-128k", "Doubao Pro 128K", "doubao", "CNY",
		5_000_000, 12_000_000, 0, 0, "字节参考价")
	add("doubao-lite-32k", "Doubao Lite 32K", "doubao", "CNY",
		300_000, 600_000, 0, 0, "字节参考价")

	// === Gemini（Google，USD） ===
	add("gemini-2.5-pro", "Gemini 2.5 Pro", "google", "USD",
		1_250_000, 10_000_000, 315_000, 0, "Google 参考价；含 reasoning")
	add("gemini-2.5-flash", "Gemini 2.5 Flash", "google", "USD",
		300_000, 2_500_000, 75_000, 0, "Google 参考价")
	add("gemini-2.0-flash", "Gemini 2.0 Flash", "google", "USD",
		100_000, 350_000, 25_000, 0, "Google 参考价")
	add("gemini-1.5-pro", "Gemini 1.5 Pro", "google", "USD",
		1_250_000, 5_000_000, 315_000, 0, "Google 参考价")
	add("gemini-1.5-flash", "Gemini 1.5 Flash", "google", "USD",
		75_000, 300_000, 18_750, 0, "Google 参考价")

	// === Qwen（阿里，CNY） ===
	add("qwen-max", "Qwen Max", "qwen", "CNY",
		40_000_000, 120_000_000, 0, 0, "阿里参考价")
	add("qwen-plus", "Qwen Plus", "qwen", "CNY",
		800_000, 2_000_000, 0, 0, "阿里参考价")
	add("qwen-turbo", "Qwen Turbo", "qwen", "CNY",
		300_000, 600_000, 0, 0, "阿里参考价")
	add("qwen-code", "Qwen Code", "qwen", "CNY",
		2_000_000, 6_000_000, 0, 0, "阿里参考价")

	return &PricingData{
		Version: 2,
		Models:  models,
		FallbackPolicy: FallbackPolicy{
			UnknownModelStrategy: "zero_cost",
			DefaultCurrency:      "USD",
			CNYToUSDFixedRate:    0.14,
		},
	}
}

// unknownModelPricing 返回一个零价格的占位 ModelPricing（用于 Resolve 失配时回退）。
// HasPrice=false 的标记由调用方维护。
func unknownModelPricing(currency string) ModelPricing {
	if currency == "" {
		currency = "USD"
	}
	return ModelPricing{
		ID:                      "unknown",
		ModelPattern:            "",
		DisplayName:             "Unknown",
		Provider:                "unknown",
		CurrencyCode:            currency,
		InputPerMillion:         0,
		OutputPerMillion:        0,
		CacheReadPerMillion:     0,
		CacheCreationPerMillion: 0,
	}
}
