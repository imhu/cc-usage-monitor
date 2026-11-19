package core

import (
	"fmt"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPricingAndTokens(t *testing.T) {
	info := ModelInfoFor("claude-opus-5-5-20260901[1m]")
	if info.DisplayName != "Opus 5.5" {
		t.Fatalf("expected Opus 5.5, got %q", info.DisplayName)
	}
	if info.Family != FamilyOpus || info.IsEstimated || info.Price == nil {
		t.Fatalf("unexpected model info: %+v", info)
	}
	toks := TokenCounts{
		Input:        1_000_000,
		Output:       500_000,
		CacheWrite5m: 1_000_000,
		CacheRead:    1_000_000,
	}
	// Opus 5.5: input=4, output=20, write5m=5, cacheRead=0.20 -> 4 + 10 + 5 + 0.20 = 19.20
	cost := info.Price.Cost(toks, false)
	if math.Abs(cost-19.20) > 1e-6 {
		t.Fatalf("expected 19.20, got %f", cost)
	}
}

func TestOfficialUsageDecodeAndFloor(t *testing.T) {
	raw := []byte(`{
		"five_hour": {"utilization": 71.9, "resets_at": "2026-11-19T12:00:00Z"},
		"seven_day": {"utilization": 10.4, "resets_at": 1795528800},
		"limits": [
			{"kind": "weekly_scoped", "percent": 0.8, "resets_at": "2026-11-21T14:00:00Z", "scope": {"model": {"display_name": "Fable"}}},
			{"kind": "other_kind", "percent": 50.0, "scope": {"model": {"display_name": "Ignored"}}}
		],
		"extra_usage": {"is_enabled": true}
	}`)
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	u, err := DecodeOfficialUsage(raw, now)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if u.FiveHour == nil || u.FiveHour.Percent() != 71 {
		t.Fatalf("expected 71%% (floored), got %+v", u.FiveHour)
	}
	if u.SevenDay == nil || u.SevenDay.Percent() != 10 {
		t.Fatalf("expected 10%%, got %+v", u.SevenDay)
	}
	if len(u.Scoped) != 1 || u.Scoped[0].ModelName != "Fable" || u.Scoped[0].Limit.Percent() != 0 {
		t.Fatalf("unexpected scoped limits: %+v", u.Scoped)
	}
}

func TestJSONLParserAndStreamingDedup(t *testing.T) {
	data := []byte(
		`{"type":"api-request","id":"req-1","timestamp":"2026-11-19T02:00:00.000Z","params":{"model":"claude-opus-5-5"}}` + "\n" +
			`{"type":"assistant","timestamp":"2026-11-19T02:00:05.000Z","requestId":"r1","requestRef":"req-1","sessionId":"s1","message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":5000}}}` + "\n" +
			`{"type":"assistant","timestamp":"2026-11-19T02:00:10.000Z","requestId":"r1","requestRef":"req-1","sessionId":"s1","message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":100,"output_tokens":710,"cache_read_input_tokens":5000}}}` + "\n" +
			`{"type":"assistant","incomplete":true`,
	)
	res := ParseJSONLBytes(data)
	if res.LinesParsed != 3 {
		t.Fatalf("expected 3 complete lines, got %d", res.LinesParsed)
	}
	idx := NewUsageIndex()
	idx.ingestEvents(res.Events, map[string]RequestStart{})
	if len(idx.Records) != 1 {
		t.Fatalf("expected 1 deduplicated record, got %d", len(idx.Records))
	}
	if idx.Records[0].Tokens.Output != 710 {
		t.Fatalf("expected final output 710, got %d", idx.Records[0].Tokens.Output)
	}
	log := idx.SpeedLog()
	if len(log.Requests) != 1 {
		t.Fatalf("expected 1 timed request, got %d", len(log.Requests))
	}
	if math.Abs(log.Requests[0].Rate()-71.0) > 1e-6 {
		t.Fatalf("expected 71 tok/s, got %f", log.Requests[0].Rate())
	}
}

func TestRequestBudgetAndCooldown(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	b := NewRequestBudget(now)
	if !b.CanAuto(now) {
		t.Fatal("fresh budget should allow auto sync")
	}
	for i := 0; i < 4; i++ {
		b.Consume(now)
	}
	if b.CanAuto(now) {
		t.Fatal("should reserve last token for manual refresh")
	}
	if !b.CanManual(now) {
		t.Fatal("manual refresh should still be allowed")
	}
	b.RecordRateLimit(now, 0)
	if !b.InCooldown(now.Add(10 * time.Minute)) {
		t.Fatal("should be in 15m cooldown after 429")
	}
	if b.Slowdown != 2 {
		t.Fatalf("expected slowdown 2, got %v", b.Slowdown)
	}
}

func TestLimitPaceAndApportion(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	reset5h := now.Add(2*time.Hour + 30*time.Minute)
	p := FiveHourPace(&reset5h, now)
	if p == nil || math.Abs(p.Fraction-0.5) > 1e-6 || p.Settling {
		t.Fatalf("unexpected 5h pace: %+v", p)
	}
	if !p.IsExceeded(0.55) || p.IsExceeded(0.45) {
		t.Fatal("IsExceeded mismatch")
	}
	shares := LargestRemainder([]float64{33.33, 33.33, 33.34}, 100)
	if shares[0]+shares[1]+shares[2] != 100 {
		t.Fatalf("shares should sum to 100: %v", shares)
	}
}

func TestMaskIPAndTrace(t *testing.T) {
	if got := MaskIP("162.141.123.45"); got != "162.141.•••.••" {
		t.Fatalf("unexpected masked IP: %q", got)
	}
	pn, ok := DecodeTrace("fl=12f\nip=162.141.1.2\nloc=US\ncolo=SJC\n")
	if !ok || pn.CountryCode != "US" || pn.RestrictsUsage() {
		t.Fatalf("unexpected trace parse: %+v %v", pn, ok)
	}
}

func TestClassifyOAuth429RetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)
	body := []byte(`{"error":{"type":"rate_limit_error","message":"Rate limited. Please try again later."}}`)

	h1 := http.Header{"Retry-After": []string{"150"}}
	err1 := classifyOAuthError(http.StatusTooManyRequests, h1, body, now)
	if !strings.Contains(err1.Error(), "2 分 30 秒（150s）") {
		t.Fatalf("expected parsed seconds in error, got: %v", err1)
	}

	h2 := http.Header{"Retry-After": []string{now.Add(5 * time.Minute).Format(http.TimeFormat)}}
	err2 := classifyOAuthError(http.StatusTooManyRequests, h2, body, now)
	if !strings.Contains(err2.Error(), "5 分钟（300s）") {
		t.Fatalf("expected parsed HTTP-date in error, got: %v", err2)
	}

	err3 := classifyOAuthError(http.StatusTooManyRequests, nil, body, now)
	if !strings.Contains(err3.Error(), "未返回 Retry-After") {
		t.Fatalf("expected fallback hint when Retry-After absent, got: %v", err3)
	}
}

func TestClaudeCodeCacheAndAutoSyncStep(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	fetchedMs := now.Add(-20 * time.Second).UnixMilli()
	raw := []byte(fmt.Sprintf(`{
		"oauthAccount": {"accountUuid": "acct-1", "emailAddress": "a@example.com"},
		"cachedUsageUtilization": {
			"fetchedAtMs": %d,
			"accountUuid": "acct-1",
			"utilization": {"five_hour": {"utilization": 25.9}, "seven_day": {"utilization": 12.1}}
		}
	}`, fetchedMs))
	cfg := ParseClaudeCodeConfig(raw)
	if cfg == nil || cfg.Usage == nil || cfg.Usage.Usage.FiveHour == nil || cfg.Usage.Usage.FiveHour.Percent() != 25 {
		t.Fatalf("failed to parse Claude Code cachedUsageUtilization: %+v", cfg)
	}
	if !cfg.Usage.IsFresh(now) {
		t.Fatal("expected 20s-old cache to be fresh")
	}
	shown := now.Add(-25 * time.Second)
	if !cfg.Usage.Replaces(&shown, "acct-1", now) {
		t.Fatal("expected newer same-account cache to replace shown")
	}
	if cfg.Usage.Replaces(&shown, "acct-other", now) {
		t.Fatal("cache from different account must not replace shown")
	}
	// Mismatched oauthAccount vs cachedUsageUtilization.accountUuid must be discarded.
	mismatch := []byte(fmt.Sprintf(`{
		"oauthAccount": {"accountUuid": "acct-2"},
		"cachedUsageUtilization": {
			"fetchedAtMs": %d,
			"accountUuid": "acct-1",
			"utilization": {"five_hour": {"utilization": 25.9}}
		}
	}`, fetchedMs))
	if mcfg := ParseClaudeCodeConfig(mismatch); mcfg == nil || mcfg.Usage != nil {
		t.Fatalf("expected mismatched account cache to be discarded, got %+v", mcfg)
	}

	// AutoSync step uses cost / (percent + 1) and takes the minimum across 5h and 7d windows.
	// 5h: 10 / (9+1) = 1.0, 7d: 20 / (24+1) = 0.8 -> min is 0.8.
	step := ComputeStep([]SyncWindow{
		{Cost: 10.0, Percent: 9},
		{Cost: 20.0, Percent: 24},
	})
	if math.Abs(step-0.8) > 1e-6 {
		t.Fatalf("expected step 0.8, got %f", step)
	}

	// RequestBudget cooldown must be capped at BudgetLongestCooldown even if clock rolls back.
	b := NewRequestBudget(now)
	b.RecordRateLimit(now, 0)
	if cd := b.Cooldown(now.Add(-24 * time.Hour)); cd != BudgetLongestCooldown {
		t.Fatalf("expected clock-rollback cooldown cap %v, got %v", BudgetLongestCooldown, cd)
	}
}
