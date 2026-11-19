package core

import (
	"sort"
	"strings"
	"sync"
)

type ModelFamily string

const (
	FamilyFable  ModelFamily = "fable"
	FamilyMythos ModelFamily = "mythos"
	FamilyOpus   ModelFamily = "opus"
	FamilySonnet ModelFamily = "sonnet"
	FamilyHaiku  ModelFamily = "haiku"
	FamilyOther  ModelFamily = "other"
)

func (f ModelFamily) DisplayName() string {
	switch f {
	case FamilyFable:
		return "Fable"
	case FamilyMythos:
		return "Mythos"
	case FamilyOpus:
		return "Opus"
	case FamilySonnet:
		return "Sonnet"
	case FamilyHaiku:
		return "Haiku"
	default:
		return "Other"
	}
}

type LongPromptTier struct {
	Above  int64
	Input  float64
	Output float64
}

type ModelPrice struct {
	Input          float64
	Output         float64
	CacheWrite5m   float64
	CacheWrite1h   float64
	CacheRead      float64
	FastMultiplier float64
	LongPrompt     *LongPromptTier
}

func newModelPrice(input, output float64, cacheRead float64, fastMul float64, lp *LongPromptTier) ModelPrice {
	if cacheRead < 0 {
		cacheRead = input * 0.1
	}
	if fastMul <= 0 {
		fastMul = 2
	}
	return ModelPrice{
		Input:          input,
		Output:         output,
		CacheWrite5m:   input * 1.25,
		CacheWrite1h:   input * 2.0,
		CacheRead:      cacheRead,
		FastMultiplier: fastMul,
		LongPrompt:     lp,
	}
}

func (p ModelPrice) Tier(t TokenCounts) ModelPrice {
	if p.LongPrompt != nil && t.Prompt() > p.LongPrompt.Above {
		return newModelPrice(p.LongPrompt.Input, p.LongPrompt.Output, -1, p.FastMultiplier, nil)
	}
	return p
}

func (p ModelPrice) Cost(t TokenCounts, fast bool) float64 {
	tier := p.Tier(t)
	micro := float64(t.Input)*tier.Input +
		float64(t.Output)*tier.Output +
		float64(t.CacheWrite5m)*tier.CacheWrite5m +
		float64(t.CacheWrite1h)*tier.CacheWrite1h +
		float64(t.CacheRead)*tier.CacheRead
	mul := 1.0
	if fast {
		mul = tier.FastMultiplier
	}
	return micro / 1_000_000.0 * mul
}

type ModelInfo struct {
	ID            string
	DisplayName   string
	Family        ModelFamily
	Price         *ModelPrice
	IsEstimated   bool
	ContextWindow int
}

const WebSearchCost = 0.01

type priceEntry struct {
	prefix  string
	price   ModelPrice
	context int
}

var (
	pricingEntries []priceEntry
	familyFallback map[ModelFamily]priceEntry
	modelCacheMu   sync.RWMutex
	modelCache     = map[string]ModelInfo{}
)

func init() {
	pricingEntries = []priceEntry{
		{"claude-fable-5-1", newModelPrice(10, 50, 0.25, 2, nil), 1_000_000},
		{"claude-mythos-5-1", newModelPrice(10, 50, 0.25, 2, nil), 1_000_000},
		{"claude-fable-5", newModelPrice(10, 50, -1, 2, nil), 1_000_000},
		{"claude-mythos-5", newModelPrice(10, 50, -1, 2, nil), 1_000_000},
		{"claude-opus-5-5", newModelPrice(4, 20, 0.20, 2, nil), 1_000_000},
		{"claude-opus-5", newModelPrice(5, 25, -1, 2, nil), 1_000_000},
		{"claude-opus-4-8", newModelPrice(5, 25, -1, 2, nil), 1_000_000},
		{"claude-opus-4-7", newModelPrice(5, 25, -1, 2, nil), 1_000_000},
		{"claude-opus-4-6", newModelPrice(5, 25, -1, 6, nil), 1_000_000},
		{"claude-opus-4-5", newModelPrice(5, 25, -1, 2, nil), 200_000},
		{"claude-opus-4-1", newModelPrice(15, 75, -1, 2, nil), 200_000},
		{"claude-opus-4", newModelPrice(15, 75, -1, 2, nil), 200_000},
		{"claude-sonnet-5-5", newModelPrice(2, 10, 0.10, 2, nil), 1_000_000},
		{"claude-sonnet-5", newModelPrice(2, 10, -1, 2, nil), 1_000_000},
		{"claude-sonnet-4-6", newModelPrice(3, 15, -1, 2, nil), 1_000_000},
		{"claude-sonnet-4-5", newModelPrice(3, 15, -1, 2, nil), 200_000},
		{"claude-sonnet-4", newModelPrice(3, 15, -1, 2, nil), 200_000},
		{"claude-3-7-sonnet", newModelPrice(3, 15, -1, 2, nil), 200_000},
		{"claude-3-5-sonnet", newModelPrice(3, 15, -1, 2, nil), 200_000},
		{"claude-haiku-5-5", newModelPrice(0.10, 0.50, -1, 2, &LongPromptTier{Above: 100_000, Input: 0.50, Output: 2.50}), 1_000_000},
		{"claude-haiku-4-5", newModelPrice(1, 5, -1, 2, nil), 200_000},
		{"claude-3-5-haiku", newModelPrice(0.8, 4, -1, 2, nil), 200_000},
		{"claude-3-haiku", newModelPrice(0.25, 1.25, -1, 2, nil), 200_000},
		{"claude-3-opus", newModelPrice(15, 75, -1, 2, nil), 200_000},
	}
	sort.Slice(pricingEntries, func(i, j int) bool {
		return len(pricingEntries[i].prefix) > len(pricingEntries[j].prefix)
	})
	find := func(prefix string) priceEntry {
		for _, e := range pricingEntries {
			if e.prefix == prefix {
				return e
			}
		}
		return priceEntry{}
	}
	familyFallback = map[ModelFamily]priceEntry{
		FamilyFable:  find("claude-fable-5-1"),
		FamilyMythos: find("claude-mythos-5-1"),
		FamilyOpus:   find("claude-opus-5-5"),
		FamilySonnet: find("claude-sonnet-5-5"),
		FamilyHaiku:  find("claude-haiku-5-5"),
	}
}

func ModelInfoFor(rawID string) ModelInfo {
	modelCacheMu.RLock()
	if hit, ok := modelCache[rawID]; ok {
		modelCacheMu.RUnlock()
		return hit
	}
	modelCacheMu.RUnlock()

	info := resolveModel(rawID)
	modelCacheMu.Lock()
	modelCache[rawID] = info
	modelCacheMu.Unlock()
	return info
}

func resolveModel(rawID string) ModelInfo {
	id := normalizeModelID(rawID)
	wantsLong := strings.Contains(strings.ToLower(rawID), "[1m]")
	fam := modelFamilyOf(id)
	name := modelDisplayName(id, fam)

	for _, e := range pricingEntries {
		if matchesModelPrefix(id, e.prefix) {
			p := e.price
			ctx := e.context
			if wantsLong && ctx < 1_000_000 {
				ctx = 1_000_000
			}
			return ModelInfo{
				ID:            rawID,
				DisplayName:   name,
				Family:        fam,
				Price:         &p,
				IsEstimated:   false,
				ContextWindow: ctx,
			}
		}
	}
	if e, ok := familyFallback[fam]; ok {
		p := e.price
		ctx := e.context
		if wantsLong {
			ctx = 1_000_000
		}
		return ModelInfo{
			ID:            rawID,
			DisplayName:   name,
			Family:        fam,
			Price:         &p,
			IsEstimated:   true,
			ContextWindow: ctx,
		}
	}
	return ModelInfo{
		ID:            rawID,
		DisplayName:   name,
		Family:        FamilyOther,
		Price:         nil,
		IsEstimated:   true,
		ContextWindow: 200_000,
	}
}

func normalizeModelID(raw string) string {
	s := strings.ToLower(raw)
	if idx := strings.Index(s, "claude-"); idx >= 0 {
		s = s[idx:]
	}
	if idx := strings.IndexAny(s, "[@:"); idx >= 0 {
		s = s[:idx]
	}
	return s
}

func matchesModelPrefix(id, prefix string) bool {
	if !strings.HasPrefix(id, prefix) {
		return false
	}
	rest := id[len(prefix):]
	return rest == "" || strings.HasPrefix(rest, "-")
}

func modelFamilyOf(id string) ModelFamily {
	for _, f := range []ModelFamily{FamilyFable, FamilyMythos, FamilyOpus, FamilySonnet, FamilyHaiku} {
		if strings.Contains(id, string(f)) {
			return f
		}
	}
	return FamilyOther
}

func modelDisplayName(id string, fam ModelFamily) string {
	if !strings.HasPrefix(id, "claude-") {
		return id
	}
	parts := strings.Split(id[len("claude-"):], "-")
	var version []string
	var name string
	for _, part := range parts {
		if part == "" {
			continue
		}
		if allDigits(part) {
			if len(part) <= 2 {
				version = append(version, part)
			}
		} else if name == "" {
			name = part
		}
	}
	title := fam.DisplayName()
	if name != "" {
		title = strings.ToUpper(name[:1]) + name[1:]
	}
	if len(version) == 0 {
		return title
	}
	return title + " " + strings.Join(version, ".")
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}
