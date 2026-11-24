package core

import "strings"

type AGModelPrice struct {
	InputPerMillion     float64
	CacheReadPerMillion float64
	OutputPerMillion    float64
}

func (p AGModelPrice) CostUSD(uncachedInput, cacheRead, output int64) float64 {
	return (float64(uncachedInput)*p.InputPerMillion +
		float64(cacheRead)*p.CacheReadPerMillion +
		float64(output)*p.OutputPerMillion) / 1_000_000.0
}

func (p AGModelPrice) CacheSavingsUSD(cacheRead int64) float64 {
	if cacheRead <= 0 {
		return 0
	}
	savedPerMillion := p.InputPerMillion - p.CacheReadPerMillion
	if savedPerMillion <= 0 {
		return 0
	}
	return float64(cacheRead) * savedPerMillion / 1_000_000.0
}

type agPricingRule struct {
	keywords []string
	price    AGModelPrice
}

// Published list prices (USD / 1M tokens) matching Antigravity Usage Monitor PricingCatalog.swift.
// Unknown models intentionally return nil (never fabricate prices).
var agPricingRules = []agPricingRule{
	// Gemini 4 family
	{keywords: []string{"gemini", "neon"}, price: AGModelPrice{InputPerMillion: 3.00, CacheReadPerMillion: 0.15, OutputPerMillion: 15.00}},
	{keywords: []string{"gemini", "hydrogen"}, price: AGModelPrice{InputPerMillion: 1.50, CacheReadPerMillion: 0.075, OutputPerMillion: 7.50}},
	{keywords: []string{"gemini", "argon"}, price: AGModelPrice{InputPerMillion: 2.00, CacheReadPerMillion: 0.10, OutputPerMillion: 10.00}},
	{keywords: []string{"gemini", "carbon"}, price: AGModelPrice{InputPerMillion: 2.00, CacheReadPerMillion: 0.10, OutputPerMillion: 10.00}},
	// Gemini 3.x family
	{keywords: []string{"gemini-3", "flash-lite"}, price: AGModelPrice{InputPerMillion: 0.25, CacheReadPerMillion: 0.025, OutputPerMillion: 1.50}},
	{keywords: []string{"gemini-3", "flash"}, price: AGModelPrice{InputPerMillion: 0.75, CacheReadPerMillion: 0.075, OutputPerMillion: 3.75}},
	{keywords: []string{"gemini-3", "pro"}, price: AGModelPrice{InputPerMillion: 2.00, CacheReadPerMillion: 0.20, OutputPerMillion: 12.00}},
	// Gemini 2.5 / generic Flash & Pro families
	{keywords: []string{"gemini", "flash-lite"}, price: AGModelPrice{InputPerMillion: 0.10, CacheReadPerMillion: 0.025, OutputPerMillion: 0.40}},
	{keywords: []string{"gemini", "flash"}, price: AGModelPrice{InputPerMillion: 0.30, CacheReadPerMillion: 0.075, OutputPerMillion: 2.50}},
	{keywords: []string{"gemini", "pro"}, price: AGModelPrice{InputPerMillion: 1.25, CacheReadPerMillion: 0.3125, OutputPerMillion: 10.00}},
	// Claude 4 / 3.7 / 3.5 families
	{keywords: []string{"claude", "opus"}, price: AGModelPrice{InputPerMillion: 15.00, CacheReadPerMillion: 1.50, OutputPerMillion: 75.00}},
	{keywords: []string{"claude", "sonnet"}, price: AGModelPrice{InputPerMillion: 3.00, CacheReadPerMillion: 0.30, OutputPerMillion: 15.00}},
	{keywords: []string{"claude", "haiku"}, price: AGModelPrice{InputPerMillion: 0.80, CacheReadPerMillion: 0.08, OutputPerMillion: 4.00}},
	// GPT families
	{keywords: []string{"gpt-5"}, price: AGModelPrice{InputPerMillion: 1.25, CacheReadPerMillion: 0.125, OutputPerMillion: 10.00}},
	{keywords: []string{"gpt-4.1"}, price: AGModelPrice{InputPerMillion: 2.00, CacheReadPerMillion: 0.50, OutputPerMillion: 8.00}},
	{keywords: []string{"gpt-4o-mini"}, price: AGModelPrice{InputPerMillion: 0.15, CacheReadPerMillion: 0.075, OutputPerMillion: 0.60}},
	{keywords: []string{"gpt-4o"}, price: AGModelPrice{InputPerMillion: 2.50, CacheReadPerMillion: 1.25, OutputPerMillion: 10.00}},
}

func AGPriceFor(model string) *AGModelPrice {
	norm := strings.ToLower(strings.TrimSpace(model))
	if norm == "" {
		return nil
	}
	for _, rule := range agPricingRules {
		matched := true
		for _, kw := range rule.keywords {
			if !strings.Contains(norm, kw) {
				matched = false
				break
			}
		}
		if matched {
			p := rule.price
			return &p
		}
	}
	return nil
}

func AGModelDisplayName(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "Unknown"
	}
	if strings.Contains(trimmed, " ") {
		return trimmed
	}
	parts := strings.Split(trimmed, "-")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		if strings.EqualFold(p, "gpt") {
			out = append(out, "GPT")
			continue
		}
		out = append(out, strings.ToUpper(p[:1])+p[1:])
	}
	if len(out) == 0 {
		return trimmed
	}
	return strings.Join(out, " ")
}

// AGContextWindowSizeFor returns the default context window size (in tokens) for a model family.
func AGContextWindowSizeFor(model string) int64 {
	norm := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(norm, "gemini"):
		return 1_048_576
	case strings.Contains(norm, "claude"):
		if strings.Contains(norm, "1m") {
			return 1_000_000
		}
		return 200_000
	case strings.Contains(norm, "gpt-4.1"):
		return 1_048_576
	case strings.Contains(norm, "gpt-5"):
		return 400_000
	case strings.Contains(norm, "gpt-4o"):
		return 128_000
	default:
		return 1_048_576
	}
}
