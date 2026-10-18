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
	// 官方价（USD/M tokens）。来源（2026-10-17 核对）：
	//   https://docs.anthropic.com/en/docs/about-claude/pricing
	// CacheCreation 按 5min writes 档入列；命中价（Hits）入 CacheRead。
	// Claude 5.5/5.1 家族为当前主线（models overview 确认 API ID）；
	// Sonnet 5 官方已确认 $2/$10 为标准价：原定 2026-09-01 涨至 $3/$15
	// 的计划已取消（定价页 claude-sonnet-5-introductory-pricing 注记）。
	add("claude-fable-5-1", "Claude Fable 5.1", "anthropic", "USD",
		10_000_000, 50_000_000, 250_000, 12_500_000,
		"官方价 2026-10-17｜docs.anthropic.com/en/docs/about-claude/pricing")
	add("claude-opus-5-5", "Claude Opus 5.5", "anthropic", "USD",
		4_000_000, 20_000_000, 200_000, 5_000_000,
		"官方价 2026-10-17｜docs.anthropic.com/en/docs/about-claude/pricing")
	add("claude-sonnet-5-5", "Claude Sonnet 5.5", "anthropic", "USD",
		2_000_000, 10_000_000, 100_000, 2_500_000,
		"官方价 2026-10-17｜docs.anthropic.com/en/docs/about-claude/pricing")
	add("claude-haiku-5-5", "Claude Haiku 5.5", "anthropic", "USD",
		100_000, 500_000, 10_000, 125_000,
		"官方价 2026-10-17｜docs.anthropic.com/en/docs/about-claude/pricing；≤100K prompt 档，超档 0.5/2.5 USD")
	add("claude-mythos-5-1", "Claude Mythos 5.1", "anthropic", "USD",
		10_000_000, 50_000_000, 250_000, 12_500_000,
		"官方价 2026-10-17｜docs.anthropic.com/en/docs/about-claude/pricing；research preview")
	add("claude-mythos-5", "Claude Mythos 5", "anthropic", "USD",
		10_000_000, 50_000_000, 1_000_000, 12_500_000,
		"官方价 2026-10-17｜docs.anthropic.com/en/docs/about-claude/pricing")
	add("claude-haiku-4-5", "Claude Haiku 4.5", "anthropic", "USD",
		1_000_000, 5_000_000, 100_000, 1_250_000,
		"官方价 2026-10-17｜docs.anthropic.com/en/docs/about-claude/pricing")
	add("claude-opus-5", "Claude Opus 5", "anthropic", "USD",
		5_000_000, 25_000_000, 500_000, 6_250_000, "Anthropic 官方价（2026-10-17 复核一致）")
	add("claude-sonnet-5", "Claude Sonnet 5", "anthropic", "USD",
		2_000_000, 10_000_000, 200_000, 2_500_000,
		"Anthropic 官方标准价（2026-10-17；原定 9 月涨至 $3/$15 已取消）")
	add("claude-fable-5", "Claude Fable 5", "anthropic", "USD",
		10_000_000, 50_000_000, 1_000_000, 12_500_000,
		"Anthropic 官方价（2026-10-17 复核一致）")
	add("claude-sonnet-4-6", "Claude Sonnet 4.6", "anthropic", "USD",
		3_000_000, 15_000_000, 300_000, 3_750_000, "Anthropic 官方价（2026-10-17 复核一致）")
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
	add("claude-3-5-haiku-latest", "Claude 3.5 Haiku (latest)", "anthropic", "USD",
		800_000, 4_000_000, 80_000, 1_000_000, "Anthropic 参考价（同 20241022 版）")
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

	// GPT-6 家族（OpenAI 官方短上下文标准价）。来源（2026-10-17 核对）：
	//   https://platform.openai.com/docs/pricing（.md 后缀可取 Markdown 表）
	// 长上下文档 input 为短档 2 倍、output 1.5 倍（astra 10/50→20/75）；Batch/Flex 半价。
	add("gpt-6-astra", "GPT-6 Astra", "openai", "USD",
		10_000_000, 50_000_000, 1_000_000, 12_500_000,
		"官方价 2026-10-17｜platform.openai.com/docs/pricing；长上下文 20/75 USD")
	add("gpt-6.1-sol", "GPT-6.1 Sol", "openai", "USD",
		2_000_000, 10_000_000, 100_000, 2_500_000,
		"官方价 2026-10-17｜platform.openai.com/docs/pricing；长上下文 4/15 USD")
	add("gpt-6-sol", "GPT-6 Sol", "openai", "USD",
		2_000_000, 10_000_000, 200_000, 2_500_000,
		"官方价 2026-10-17｜platform.openai.com/docs/pricing；长上下文 4/15 USD")
	add("gpt-6-luna", "GPT-6 Luna", "openai", "USD",
		100_000, 500_000, 10_000, 125_000,
		"官方价 2026-10-17｜platform.openai.com/docs/pricing；长上下文 0.2/0.75 USD")

	// GPT-5.x 系列（OpenAI 官方短上下文标准价，2026-10-17 复核更新）。
	// gpt-5.6 三档价格官方已下调（sol 4/20，terra 2/12，luna 0.2/1.2）；
	// cacheCreation 为 0 的模型官方未公布 cache writes 计费，按 0 入库。
	add("gpt-5.6-sol", "GPT-5.6 Sol", "openai", "USD",
		4_000_000, 20_000_000, 400_000, 5_000_000,
		"官方价 2026-10-17｜platform.openai.com/docs/pricing；长上下文 8/30 USD")
	add("gpt-5.6-terra", "GPT-5.6 Terra", "openai", "USD",
		2_000_000, 12_000_000, 200_000, 2_500_000,
		"官方价 2026-10-17｜platform.openai.com/docs/pricing；长上下文 4/18 USD")
	add("gpt-5.6-luna", "GPT-5.6 Luna", "openai", "USD",
		200_000, 1_200_000, 20_000, 250_000,
		"官方价 2026-10-17｜platform.openai.com/docs/pricing；长上下文 0.4/1.8 USD")
	add("gpt-5.5", "GPT-5.5", "openai", "USD",
		5_000_000, 30_000_000, 500_000, 0,
		"OpenAI 官方短上下文标准价，参考价")
	add("gpt-5.5-pro", "GPT-5.5 Pro", "openai", "USD",
		30_000_000, 180_000_000, 30_000_000, 0,
		"OpenAI 官方价；cache read 无折扣（同 input 价）")
	add("gpt-5.4", "GPT-5.4", "openai", "USD",
		2_500_000, 15_000_000, 250_000, 0,
		"OpenAI 官方短上下文标准价，参考价")
	add("gpt-5.4-mini", "GPT-5.4 mini", "openai", "USD",
		750_000, 4_500_000, 75_000, 0,
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
		2_000_000, 8_000_000, 500_000, 0, "OpenAI 官方价（2026-10-17 复核，已大幅降价）")
	add("o3-mini", "OpenAI o3 mini", "openai", "USD",
		1_100_000, 4_400_000, 550_000, 0, "OpenAI 官方价（2026-10-17 复核，已大幅降价）")

	// === GLM 系（智谱，CNY） ===
	// 国产模型按 CNY 计价（设计 6.5）。来源（2026-10-17 核对）：
	//   https://docs.bigmodel.cn/cn/guide/start/pricing
	// 多数型号阶梯计价，入库取最低档，阶梯在备注；缓存命中价入 CacheRead；
	// GLM 缓存存储限时免费、无缓存写入计费，CacheCreation 恒为 0。
	add("glm-5.3", "GLM-5.3", "glm", "CNY",
		8_000_000, 28_000_000, 2_000_000, 0,
		"官方价 2026-10-17｜docs.bigmodel.cn/cn/guide/start/pricing；1M 上下文")
	add("glm-5.3-flash", "GLM-5.3 Flash", "glm", "CNY",
		800_000, 2_800_000, 230_000, 0,
		"官方价 2026-10-17｜docs.bigmodel.cn/cn/guide/start/pricing；多模态，1M 上下文")
	add("glm-5.3-flashx", "GLM-5.3 FlashX", "glm", "CNY",
		2_000_000, 7_000_000, 570_000, 0,
		"官方价 2026-10-17｜docs.bigmodel.cn/cn/guide/start/pricing；多模态，1M 上下文")
	add("glm-5.2", "GLM-5.2", "glm", "CNY",
		8_000_000, 28_000_000, 2_000_000, 0, "智谱官方价（2026-10-17；1M 上下文）")
	add("glm-5.1", "GLM-5.1", "glm", "CNY",
		6_000_000, 24_000_000, 1_300_000, 0,
		"智谱官方价（2026-10-17；输入<32K 档，≥32K 同 GLM-5.2 价）")
	add("glm-5", "GLM-5", "glm", "CNY",
		4_000_000, 18_000_000, 1_000_000, 0,
		"智谱官方价（2026-10-17；输入<32K 档，≥32K 6/22 CNY）")
	add("glm-5-turbo", "GLM-5 Turbo", "glm", "CNY",
		5_000_000, 22_000_000, 1_200_000, 0,
		"智谱官方价（2026-10-17；输入<32K 档，≥32K 7/26 CNY）")
	add("glm-4.7", "GLM-4.7", "glm", "CNY",
		2_000_000, 8_000_000, 400_000, 0,
		"智谱官方价（2026-10-17；阶梯：出≥0.2K 3/14，入≥32K 4/16 CNY）")
	add("glm-4.5-air", "GLM-4.5 Air", "glm", "CNY",
		800_000, 2_000_000, 160_000, 0,
		"智谱官方价（2026-10-17；最低档，阶梯见定价页）")
	add("glm-4-plus", "GLM-4 Plus", "glm", "CNY",
		5_000_000, 5_000_000, 2_500_000, 0, "智谱官方价（2026-10-17）")
	add("glm-4.6", "GLM-4.6", "glm", "CNY",
		2_000_000, 8_000_000, 200_000, 0, "智谱历史参考价（已从官方定价页移除）")
	add("glm-4.6-air", "GLM-4.6 Air", "glm", "CNY",
		500_000, 500_000, 50_000, 0, "智谱历史参考价（已从官方定价页移除）")
	add("glm-4.5", "GLM-4.5", "glm", "CNY",
		2_000_000, 8_000_000, 200_000, 0, "智谱历史参考价（已从官方定价页移除）")
	add("glm-4-flash", "GLM-4 Flash", "glm", "CNY",
		0, 0, 0, 0, "智谱免费模型")
	add("glm-4-long", "GLM-4 Long", "glm", "CNY",
		1_000_000, 1_000_000, 500_000, 0, "智谱官方价（2026-10-17；1M 上下文）")
	add("glm-4v", "GLM-4V", "glm", "CNY",
		50_000_000, 50_000_000, 0, 0, "智谱视觉模型参考价")
	add("glm-4v-plus", "GLM-4V Plus", "glm", "CNY",
		10_000_000, 10_000_000, 0, 0, "智谱视觉模型参考价")

	// === DeepSeek（深度求索；旧型号 CNY，现行 V4 系列 USD） ===
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
	// DeepSeek 现行 API（2026-10-17 核对）：
	//   https://api-docs.deepseek.com/quick_start/pricing
	// 官方仅两款：deepseek-flash（V4.1-Flash-1017）与 deepseek-v4-pro
	// （V4-Pro-0813）；错峰价为高峰的一半，入库取错峰价，高峰价在备注。
	add("deepseek-flash", "DeepSeek Flash", "deepseek", "USD",
		150_000, 600_000, 3_000, 0,
		"官方价 2026-10-17｜api-docs.deepseek.com/quick_start/pricing；"+"错峰价，高峰 0.3/1.2 USD；1M 上下文")
	// DeepSeek-V4-Pro 现价（2026-10-17）：错峰 cache miss $0.66 / hit $0.022 /
	// output $1.98；高峰翻倍。早期 2026-07 价（0.435/0.87）已作废。
	add("deepseek-v4-pro", "DeepSeek V4 Pro", "deepseek", "USD",
		660_000, 1_980_000, 22_000, 0,
		"DeepSeek API 官方价（2026-10-17，错峰；高峰 1.32/3.96，hit 0.044 USD）")
	// deepseek-v4-flash / -vision-exp 已退役，请求由 V4.1-Flash 接替并按
	// Flash 价计费（官方定价页脚注(1)）；保留旧模式名供历史流量归因计价。
	add("deepseek-v4-flash", "DeepSeek V4 Flash", "deepseek", "USD",
		150_000, 600_000, 3_000, 0,
		"已退役，按 Flash 错峰价计费（2026-10-17；高峰 0.3/1.2 USD）")

	// === Kimi / Moonshot（月之暗面，CNY） ===
	add("moonshot-v1-8k", "Moonshot v1 8K", "moonshot", "CNY",
		12_000_000, 12_000_000, 0, 0, "Moonshot 参考价")
	add("moonshot-v1-32k", "Moonshot v1 32K", "moonshot", "CNY",
		24_000_000, 24_000_000, 0, 0, "Moonshot 参考价")
	add("moonshot-v1-128k", "Moonshot v1 128K", "moonshot", "CNY",
		60_000_000, 60_000_000, 0, 0, "Moonshot 参考价")
	add("kimi-k2", "Kimi K2", "moonshot", "CNY",
		4_000_000, 16_000_000, 0, 0, "Moonshot 参考价")
	add("kimi-k2.5", "Kimi K2.5", "moonshot", "CNY",
		4_000_000, 16_000_000, 700_000, 0, "Moonshot 官方价（模型已下线，历史用量）")
	add("kimi-k2.6", "Kimi K2.6", "moonshot", "CNY",
		6_500_000, 27_000_000, 1_100_000, 0, "Moonshot 官方价（2026-10-17 复核一致）")
	// Kimi K2.7 Code（2026-10-17 官方价）：缓存未命中 ¥6.5 / 命中 ¥1.3 /
	// 输出 ¥27；highspeed 档全价翻倍。262K 上下文，无缓存写入计费。
	add("kimi-k2.7-code", "Kimi K2.7 Code", "moonshot", "CNY",
		6_500_000, 27_000_000, 1_300_000, 0,
		"官方价 2026-10-17｜platform.kimi.com/docs/pricing/chat；262K 上下文")
	add("kimi-k2.7-code-highspeed", "Kimi K2.7 Code Highspeed", "moonshot", "CNY",
		13_000_000, 54_000_000, 2_600_000, 0,
		"官方价 2026-10-17｜platform.kimi.com/docs/pricing/chat；高速档")
	// Kimi K3（官方价，1M 上下文）：缓存写入按 TTL 单独计费（5min ¥20 /
	// 1h ¥40），CacheCreation 取 5min 档；中转/聚合常见 k3、k3-256k
	// 短名，官方定价页未单列，沿用 kimi-k3 官方 token 价避免落入 unknown。
	add("kimi-k3", "Kimi K3", "moonshot", "CNY",
		20_000_000, 100_000_000, 2_000_000, 20_000_000,
		"官方价 2026-10-17｜platform.kimi.com/docs/pricing/chat；缓存写 5min ¥20/1h ¥40")
	add("kimi-k3-256k", "Kimi K3 (256K)", "moonshot", "CNY",
		20_000_000, 100_000_000, 2_000_000, 0, "中转/聚合变体，官方定价页未单列；沿用 kimi-k3 官方 token 价，缓存写入未核实")
	add("k3", "Kimi K3（中转短名）", "moonshot", "CNY",
		20_000_000, 100_000_000, 2_000_000, 0, "中转/聚合短名，官方定价页未单列；沿用 kimi-k3 官方 token 价，缓存写入未核实")
	add("k3-256k", "Kimi K3 256K（中转短名）", "moonshot", "CNY",
		20_000_000, 100_000_000, 2_000_000, 0, "中转/聚合短名，官方定价页未单列；沿用 kimi-k3 官方 token 价，缓存写入未核实")

	// === MiniMax（CNY） ===
	// 来源（2026-10-17 核对）：https://platform.minimaxi.com/docs/pricing/overview
	add("abab6.5-chat", "ABAB 6.5 Chat", "minimax", "CNY",
		30_000_000, 30_000_000, 0, 0, "MiniMax 历史参考价（已下线）")
	add("minimax-text-01", "MiniMax Text 01", "minimax", "CNY",
		2_000_000, 2_000_000, 0, 0, "MiniMax 历史参考价")
	// MiniMax-M3（官方价，永久五折后）：≤512K 输入 2.1/8.4，>512K 翻倍；
	// priority 层（service_tier=priority）按 1.5 倍计费；无缓存写入计费。
	add("minimax-m3", "MiniMax M3", "minimax", "CNY",
		2_100_000, 8_400_000, 420_000, 0,
		"官方价 2026-10-17｜platform.minimaxi.com/docs/pricing/overview；"+"永久五折价，>512K 输入翻倍")
	add("minimax-m2.7", "MiniMax M2.7", "minimax", "CNY",
		2_100_000, 8_400_000, 420_000, 2_625_000, "MiniMax 官方价（2026-10-17 复核一致）；缓存读 0.42/写 2.625 CNY")
	add("minimax-m2.7-highspeed", "MiniMax M2.7 Highspeed", "minimax", "CNY",
		4_200_000, 16_800_000, 420_000, 2_625_000,
		"官方价 2026-10-17｜platform.minimaxi.com/docs/pricing/overview；高速档")

	// === Doubao（字节跳动，CNY） ===
	add("doubao-pro-32k", "Doubao Pro 32K", "doubao", "CNY",
		800_000, 2_000_000, 0, 0, "字节参考价")
	add("doubao-pro-128k", "Doubao Pro 128K", "doubao", "CNY",
		5_000_000, 12_000_000, 0, 0, "字节参考价")
	add("doubao-lite-32k", "Doubao Lite 32K", "doubao", "CNY",
		300_000, 600_000, 0, 0, "字节参考价")

	// === Gemini（Google，USD） ===
	// Gemini 3.x Flash（2026 促销价，至 2026-12-31；缓存存储按时间另计）。
	// gemini-3.7-flash 前缀同时覆盖 -safety-le / -control 服务变体。
	add("gemini-3.8-flash", "Gemini 3.8 Flash", "google", "USD",
		750_000, 3_750_000, 75_000, 0,
		"Google 促销价（至 2026-12-31，2027 起 1.5/7.5 USD）；缓存存储另计")
	add("gemini-3.7-flash", "Gemini 3.7 Flash", "google", "USD",
		750_000, 3_750_000, 75_000, 0,
		"Google 促销价（至 2026-12-31，2027 起 1.5/7.5 USD）；缓存存储另计")
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

	// === Qwen（阿里云百炼，CNY） ===
	// 来源（2026-10-17 核对）：
	//   价格表：https://help.aliyun.com/zh/model-studio/model-pricing
	//   隐式缓存命中按输入单价 20%：《上下文缓存》官方规则
	//   （https://help.aliyun.com/zh/model-studio/context-cache）
	// 阶梯计价模型取常用档入库，完整阶梯在备注；Batch 半价不入库。
	add("qwen3.8-max", "Qwen3.8 Max", "qwen", "CNY",
		12_000_000, 36_000_000, 2_400_000, 0,
		"官方价 2026-10-17｜help.aliyun.com/zh/model-studio/model-pricing；"+"输入≤1M；隐式缓存命中 20%")
	add("qwen3.7-plus", "Qwen3.7 Plus", "qwen", "CNY",
		2_000_000, 8_000_000, 400_000, 0,
		"官方价 2026-10-17｜help.aliyun.com/zh/model-studio/model-pricing；"+"<256K 档，≥256K 6/24 CNY")
	add("qwen3.8-flash", "Qwen3.8 Flash", "qwen", "CNY",
		800_000, 2_700_000, 160_000, 0,
		"官方价 2026-10-17｜help.aliyun.com/zh/model-studio/model-pricing；输入≤1M")
	add("qwen3.7-flash", "Qwen3.7 Flash", "qwen", "CNY",
		200_000, 800_000, 40_000, 0,
		"官方价 2026-10-17｜help.aliyun.com/zh/model-studio/model-pricing；"+"<32K 档，阶梯至 1.2/4.8 CNY")
	add("qwen3-coder-flash", "Qwen3 Coder Flash", "qwen", "CNY",
		1_500_000, 6_000_000, 300_000, 0,
		"官方价 2026-10-17｜help.aliyun.com/zh/model-studio/model-pricing；"+"32K-128K 档，阶梯 1/4→5/25 CNY")
	add("qwen-max", "Qwen Max", "qwen", "CNY",
		2_400_000, 9_600_000, 0, 0, "阿里官方价（2026-10-17；仅非思考模式，无阶梯）")
	add("qwen-plus", "Qwen Plus", "qwen", "CNY",
		800_000, 2_000_000, 160_000, 0,
		"阿里官方价（2026-10-17；非思考档，思维链+回答输出 8 CNY）")
	add("qwen-turbo", "Qwen Turbo", "qwen", "CNY",
		300_000, 600_000, 0, 0, "阿里历史参考价（已从官方定价页移除）")
	add("qwen-code", "Qwen Code", "qwen", "CNY",
		2_000_000, 6_000_000, 0, 0, "阿里历史参考价（已从官方定价页移除）")
	add("qwen3-coder-plus", "Qwen3 Coder Plus", "qwen", "CNY",
		4_000_000, 16_000_000, 400_000, 5_000_000,
		"阿里官方价；隐式缓存读 0.4/写 5 CNY（已从当前定价页移除，历史用量）")
	add("qwen-2.5-72b-instruct", "Qwen2.5 72B Instruct", "qwen", "USD",
		360_000, 400_000, 0, 0, "OpenRouter 参考价（USD）")

	// === xAI（Grok，USD） ===
	add("grok-4.5", "Grok 4.5", "xai", "USD",
		2_000_000, 6_000_000, 300_000, 0, "xAI 官方价（短上下文；错峰减半）")
	add("grok-4.6", "Grok 4.6", "xai", "USD",
		2_000_000, 6_000_000, 500_000, 0, "xAI 官方价")

	// === 中转/免费杂项 ===
	add("ox-alpha-free", "OX Alpha (free)", "openrouter", "USD",
		0, 0, 0, 0, "OpenRouter 隐身免费模型（推测 GLM-5.3-Flash 底座）；零价")

	return &PricingData{
		Version: 3,
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
