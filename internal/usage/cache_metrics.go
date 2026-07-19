package usage

// completeTokenCount returns a non-overlapping quantity across providers.
// Codex's input includes cache reads whereas Claude's does not, so callers use
// BillableInput (fresh input) and then add cache dimensions exactly once.
func completeTokenCount(billableInput, output, cacheRead, cacheCreation int64) int64 {
	return nonNegative(billableInput) + nonNegative(output) +
		nonNegative(cacheRead) + nonNegative(cacheCreation)
}

func nonNegative(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

// applyModelPricingMetrics derives cache economics from a model's active
// pricing rule. Raw token counts remain visible; the normalized token value is
// explicitly price-adjusted and never replaces raw usage.
func applyModelPricingMetrics(m *ModelStat, pricing ModelPricing) {
	if m == nil {
		return
	}
	m.TotalTokens = completeTokenCount(m.BillableInput, m.OutputTokens, m.CacheRead, m.CacheCreation)
	eligibleInput := nonNegative(m.BillableInput) + nonNegative(m.CacheRead)
	if eligibleInput > 0 {
		m.CacheHitRate = float64(nonNegative(m.CacheRead)) / float64(eligibleInput)
	}

	// Unknown/free models have no meaningful price-equivalent conversion. Keep
	// their effective count equal to the complete raw count rather than invent a
	// discount that the user did not configure.
	if pricing.InputPerMillion <= 0 {
		m.CacheAdjustedTokens = m.TotalTokens
		return
	}

	cacheReadEquivalent := scaleTokensByPrice(m.CacheRead, pricing.CacheReadPerMillion, pricing.InputPerMillion)
	cacheCreationEquivalent := scaleTokensByPrice(m.CacheCreation, pricing.CacheCreationPerMillion, pricing.InputPerMillion)
	m.CacheAdjustedTokens = nonNegative(m.BillableInput) + cacheReadEquivalent + cacheCreationEquivalent + nonNegative(m.OutputTokens)

	// This is calculated from the live price table, rather than a stored cost
	// split, so it is also available for sources that provide only a total bill.
	m.CacheReadEstimatedCost = mulDivInt64(nonNegative(m.CacheRead), pricing.CacheReadPerMillion, 1_000_000)
	fullPriceCacheRead := mulDivInt64(nonNegative(m.CacheRead), pricing.InputPerMillion, 1_000_000)
	if fullPriceCacheRead > m.CacheReadEstimatedCost {
		m.CacheHitSavings = fullPriceCacheRead - m.CacheReadEstimatedCost
	}
}

func scaleTokensByPrice(tokens, discountedPrice, fullInputPrice int64) int64 {
	if tokens <= 0 || discountedPrice <= 0 || fullInputPrice <= 0 {
		return 0
	}
	return tokens * discountedPrice / fullInputPrice
}
