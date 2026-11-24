package core

import (
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
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
	idx.ingestEvents(res.Events, "session.jsonl")
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
	// Consume last manual token right before hitting 429: manual reserve must still be preserved after cooldown.
	b.Consume(now)
	b.RecordRateLimit(now, 0)
	if !b.InCooldown(now.Add(10 * time.Minute)) {
		t.Fatal("should be in 15m cooldown after 429")
	}
	if rw := b.RetryWait(now); rw != BudgetFirstCooldown {
		t.Fatalf("expected initial RetryWait=%v (cooldown), got %v", BudgetFirstCooldown, rw)
	}
	if b.Slowdown != 2 {
		t.Fatalf("expected slowdown 2, got %v", b.Slowdown)
	}
	// Right after 15m cooldown ends, manual refresh must be allowed and RetryWait must clear to 0,
	// while auto sync still waits for slowed refill (<= 40m).
	after15 := now.Add(15 * time.Minute)
	if b.InCooldown(after15) || b.RetryWait(after15) != 0 {
		t.Fatalf("expected cooldown & RetryWait cleared at +15m, got %v", b.RetryWait(after15))
	}
	if !b.CanManual(after15) {
		t.Fatal("manual refresh should be allowed right after cooldown ends")
	}
	if b.CanAuto(after15) {
		t.Fatal("auto sync should still wait for slowed refill after 429")
	}
	if w := b.Wait(false, now); w > 40*time.Minute {
		t.Fatalf("expected auto wait <= 40m after first 429, got %v", w)
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

func TestMidweekResetBetween(t *testing.T) {
	wEnd := time.Date(2026, 11, 22, 6, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

	// Same week, chronological, >= 5 point drop -> detected.
	r := MidweekResetBetween(WeeklyReading{Utilization: 95, At: t1}, wEnd, WeeklyReading{Utilization: 1, At: t2}, wEnd.Add(30*time.Second))
	if r == nil || !r.BelongsToWeekEnding(wEnd) {
		t.Fatalf("expected MidweekReset, got %+v", r)
	}
	// Exact 5 point drop boundary -> detected.
	if MidweekResetBetween(WeeklyReading{Utilization: 20, At: t1}, wEnd, WeeklyReading{Utilization: 15, At: t2}, wEnd) == nil {
		t.Fatal("expected exact 5-point drop to trigger MidweekReset")
	}
	// Drop < 5 points -> ignored.
	if MidweekResetBetween(WeeklyReading{Utilization: 20, At: t1}, wEnd, WeeklyReading{Utilization: 15.1, At: t2}, wEnd) != nil {
		t.Fatal("drop < 5 points must not trigger MidweekReset")
	}
	// Different week (>= 60s apart) -> ignored.
	if MidweekResetBetween(WeeklyReading{Utilization: 95, At: t1}, wEnd, WeeklyReading{Utilization: 1, At: t2}, wEnd.Add(7*24*time.Hour)) != nil {
		t.Fatal("next week reset must not trigger MidweekReset")
	}
	// Non-chronological -> ignored.
	if MidweekResetBetween(WeeklyReading{Utilization: 95, At: t2}, wEnd, WeeklyReading{Utilization: 1, At: t1}, wEnd) != nil {
		t.Fatal("out-of-order readings must not trigger MidweekReset")
	}
	// NaN utilization -> ignored.
	if MidweekResetBetween(WeeklyReading{Utilization: math.NaN(), At: t1}, wEnd, WeeklyReading{Utilization: 1, At: t2}, wEnd) != nil {
		t.Fatal("NaN utilization must not trigger MidweekReset")
	}
}

func makeOpus55Record(at time.Time, costUSD float64) UsageRecord {
	// Opus 5.5 input is $4 / 1M tokens -> 250,000 tokens per $1.
	return UsageRecord{
		Time:     unixSec(at),
		ModelIdx: 0,
		Tokens:   TokenCounts{Input: int64(math.Round(costUSD * 250_000))},
	}
}

func TestMidweekResetAllowanceAndProjection(t *testing.T) {
	wStart := time.Date(2026, 11, 15, 6, 0, 0, 0, time.UTC)
	wEnd := wStart.Add(WeekWindow) // Nov 22 06:00
	tBefore := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	tAfter := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	now := time.Date(2026, 11, 18, 15, 0, 0, 0, time.UTC)

	// Pre-reset: $500 before tBefore (50% => $10/pct, $1000 full).
	// Between tBefore and tAfter: $30 (pre-reset), $10 at 09:00, $10 at 11:00.
	// Post-sync: $40 at 14:00. Total week cost = $590.
	tPost1 := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	idx := IndexSnapshot{
		Models: []string{"claude-opus-5-5"},
		Records: []UsageRecord{
			makeOpus55Record(time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC), 500),
			makeOpus55Record(time.Date(2026, 11, 17, 15, 0, 0, 0, time.UTC), 30),
			makeOpus55Record(tPost1, 10),
			makeOpus55Record(time.Date(2026, 11, 18, 11, 0, 0, 0, time.UTC), 10),
			makeOpus55Record(time.Date(2026, 11, 18, 14, 0, 0, 0, time.UTC), 40),
		},
	}

	reset := MidweekResetBetween(
		WeeklyReading{Utilization: 50, At: tBefore}, wEnd,
		WeeklyReading{Utilization: 2, At: tAfter}, wEnd,
	)
	if reset == nil {
		t.Fatal("expected valid MidweekReset")
	}

	settings := UsageSettings{
		BillingAnchorDay: 1,
		WeeklyReset:      &wEnd,
		WeeklyReading:    &WeeklyReading{Utilization: 2, At: tAfter},
		EarlierAllowance: &AllowanceBasis{Utilization: 50, SyncedCost: 500, At: tBefore},
		MidweekReset:     reset,
	}
	snap := ComputeSnapshot(idx, settings, now)
	a := snap.WeeklyAllowance
	if a == nil {
		t.Fatal("expected WeeklyAllowance")
	}
	if a.RestartedAt == nil || !a.RestartedAt.Equal(tPost1) {
		t.Fatalf("expected RestartedAt=%v, got %v", tPost1, a.RestartedAt)
	}
	if math.Abs(a.Forgiven-530) > 1e-6 {
		t.Fatalf("expected Forgiven=530, got %f", a.Forgiven)
	}
	if math.Abs(a.Spent-60) > 1e-6 {
		t.Fatalf("expected post-reset Spent=60, got %f", a.Spent)
	}
	if !a.Borrowed {
		t.Fatal("expected borrowed earlier allowance while post-reset utilization < 10%")
	}
	if math.Abs(a.Full()-1000) > 1e-6 {
		t.Fatalf("expected Full=1000, got %f", a.Full())
	}
	if math.Abs(a.Remaining()-940) > 1e-6 {
		t.Fatalf("expected Remaining=940, got %f", a.Remaining())
	}
	if math.Abs(a.FullWeek()-1530) > 1e-6 {
		t.Fatalf("expected FullWeek=1530, got %f", a.FullWeek())
	}
	proj := snap.Week.Projection(now, a)
	if proj == nil || math.Abs(proj.Cost-1530) > 1e-6 {
		t.Fatalf("expected week projection=1530, got %+v", proj)
	}

	// Fallback detection when MidweekReset was not pre-populated (upgrade path).
	settings.MidweekReset = nil
	snapFallback := ComputeSnapshot(idx, settings, now)
	af := snapFallback.WeeklyAllowance
	if af == nil || af.Reset == nil {
		t.Fatalf("expected fallback MidweekReset detection, got %+v", af)
	}
	if math.Abs(af.Forgiven-530) > 1e-6 || math.Abs(af.FullWeek()-1530) > 1e-6 {
		t.Fatalf("fallback mismatch: forgiven=%f fullWeek=%f", af.Forgiven, af.FullWeek())
	}

	// Once post-reset utilization reaches >= 10%, use current post-reset ratio directly.
	settings.MidweekReset = reset
	settings.WeeklyReading = &WeeklyReading{Utilization: 12, At: now}
	snapPost := ComputeSnapshot(idx, settings, now)
	ap := snapPost.WeeklyAllowance
	if ap == nil || ap.Borrowed {
		t.Fatalf("expected non-borrowed allowance at 12%%, got %+v", ap)
	}
	// Post-reset synced cost = $60 at 12% => Full = $500, FullWeek = $530 + $500 = $1030.
	if math.Abs(ap.Full()-500) > 1e-6 || math.Abs(ap.FullWeek()-1030) > 1e-6 {
		t.Fatalf("unexpected post-reset 12%% allowance: full=%f fullWeek=%f", ap.Full(), ap.FullWeek())
	}
}

func TestWeeklyAllowanceAdmitsSanityCheck(t *testing.T) {
	wStart := time.Date(2026, 11, 15, 6, 0, 0, 0, time.UTC)
	wEnd := wStart.Add(WeekWindow)
	tSync := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	now := time.Date(2026, 11, 16, 13, 0, 0, 0, time.UTC)
	prevWeekAt := wStart.Add(-24 * time.Hour)

	// Current week: $50 at 5% => low = 50*100/6 = 833.33, high = 50*100/5 = 1000.
	// Admissible earlier.Full() range is [416.67, 2000].
	idx := IndexSnapshot{
		Models:  []string{"claude-opus-5-5"},
		Records: []UsageRecord{makeOpus55Record(time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC), 50)},
	}

	base := UsageSettings{
		BillingAnchorDay: 1,
		WeeklyReset:      &wEnd,
		WeeklyReading:    &WeeklyReading{Utilization: 5, At: tSync},
	}

	// Consistent earlier basis ($1200 full) -> borrowed.
	sOK := base
	sOK.EarlierAllowance = &AllowanceBasis{Utilization: 50, SyncedCost: 600, At: prevWeekAt}
	aOK := ComputeSnapshot(idx, sOK, now).WeeklyAllowance
	if aOK == nil || !aOK.Borrowed || math.Abs(aOK.Full()-1200) > 1e-6 {
		t.Fatalf("expected consistent earlier basis ($1200) to be borrowed, got %+v", aOK)
	}

	// Earlier basis > 2x high ($2500 > $2000, e.g. downgraded plan) -> rejected, keep current ($1000).
	sHigh := base
	sHigh.EarlierAllowance = &AllowanceBasis{Utilization: 40, SyncedCost: 1000, At: prevWeekAt}
	aHigh := ComputeSnapshot(idx, sHigh, now).WeeklyAllowance
	if aHigh == nil || aHigh.Borrowed || math.Abs(aHigh.Full()-1000) > 1e-6 {
		t.Fatalf("expected oversized earlier basis ($2500) to be rejected, got %+v", aHigh)
	}

	// Earlier basis < low/2 ($300 < $416.67, e.g. upgraded plan) -> rejected, keep current ($1000).
	sLow := base
	sLow.EarlierAllowance = &AllowanceBasis{Utilization: 50, SyncedCost: 150, At: prevWeekAt}
	aLow := ComputeSnapshot(idx, sLow, now).WeeklyAllowance
	if aLow == nil || aLow.Borrowed || math.Abs(aLow.Full()-1000) > 1e-6 {
		t.Fatalf("expected undersized earlier basis ($300) to be rejected, got %+v", aLow)
	}

	// Direct boundary check on Admits.
	probe := WeeklyAllowance{Utilization: 5, SyncedCost: 50}
	low := 50.0 * 100.0 / 6.0
	high := 50.0 * 100.0 / 5.0
	if !probe.Admits(low/2.0) || !probe.Admits(high*2.0) {
		t.Fatal("expected exact boundary values to be admitted")
	}
	if probe.Admits(low/2.0-0.01) || probe.Admits(high*2.0+0.01) {
		t.Fatal("expected values just outside 2x band to be rejected")
	}
	if !probe.Admits(0) || !probe.Admits(math.NaN()) || !probe.Admits(math.Inf(1)) {
		t.Fatal("expected non-positive / non-finite guard to return true")
	}
}

func TestSpeedTimingStreamingZeroFirstChunkInterruptAndStartupPrime(t *testing.T) {
	tmp := t.TempDir()
	sessionFile := filepath.Join(tmp, "session.jsonl")
	now := time.Now().UTC().Truncate(time.Second)
	t0 := now.Add(-2 * time.Minute)
	t1 := t0.Add(5 * time.Second)
	t2 := now.Add(-1 * time.Minute)
	t3 := t2.Add(2 * time.Second)
	t4 := now.Add(-30 * time.Second)

	// 1. Streaming chunk 1 has output_tokens=0 and same timestamp as api-request;
	//    chunk 2 arrives 5s later with 500 output_tokens -> must NOT lose start time!
	part1 := fmt.Sprintf(
		`{"type":"api-request","id":"req-1","timestamp":"%s","params":{"model":"claude-opus-5-5"}}`+"\n"+
			`{"type":"assistant","timestamp":"%s","requestId":"r1","requestRef":"req-1","sessionId":"s1","message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":100,"output_tokens":0}}}`+"\n",
		t0.Format(time.RFC3339), t0.Format(time.RFC3339),
	)
	if err := os.WriteFile(sessionFile, []byte(part1), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := NewUsageIndex()
	idx.ScanRoots([]string{tmp}, nil)

	// Chunk 2 arrives in a subsequent incremental scan.
	part2 := part1 + fmt.Sprintf(
		`{"type":"assistant","timestamp":"%s","requestId":"r1","requestRef":"req-1","sessionId":"s1","message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":100,"output_tokens":500}}}`+"\n",
		t1.Format(time.RFC3339),
	)
	if err := os.WriteFile(sessionFile, []byte(part2), 0o644); err != nil {
		t.Fatal(err)
	}
	idx.ScanRoots([]string{tmp}, nil)

	log := idx.SpeedLog()
	if len(log.Requests) != 1 || log.Requests[0].Output != 500 || math.Abs(log.Requests[0].Rate()-100.0) > 1e-6 {
		t.Fatalf("expected timed request 500 tokens @ 100 tok/s after 0-output first chunk, got %+v", log.Requests)
	}

	// 2. Request starts in scan A, then gets interrupted in scan B -> Pending must clear across scans!
	part3 := part2 + fmt.Sprintf(
		`{"type":"api-request","id":"req-2","timestamp":"%s","params":{"model":"claude-opus-5-5"}}`+"\n",
		t2.Format(time.RFC3339),
	)
	if err := os.WriteFile(sessionFile, []byte(part3), 0o644); err != nil {
		t.Fatal(err)
	}
	idx.ScanRoots([]string{tmp}, nil)
	if len(idx.SpeedLog().Pending) != 1 {
		t.Fatalf("expected 1 pending request, got %+v", idx.SpeedLog().Pending)
	}

	part4 := part3 + fmt.Sprintf(
		`{"type":"user","timestamp":"%s","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`+"\n",
		t3.Format(time.RFC3339),
	)
	if err := os.WriteFile(sessionFile, []byte(part4), 0o644); err != nil {
		t.Fatal(err)
	}
	stats := idx.ScanRoots([]string{tmp}, nil)
	if !stats.TimingsChanged {
		t.Fatal("expected TimingsChanged=true when pending request is interrupted")
	}
	if len(idx.SpeedLog().Pending) != 0 {
		t.Fatalf("expected 0 pending requests after interrupt in next scan, got %+v", idx.SpeedLog().Pending)
	}

	// 3. New api-request in same file replaces unclosed pending (never piles up duplicates),
	//    and startup priming recovers in-flight pending & starts after LoadUsageIndex!
	part5 := part4 + fmt.Sprintf(
		`{"type":"api-request","id":"req-3a","timestamp":"%s","params":{"model":"claude-opus-5-5"}}`+"\n"+
			`{"type":"api-request","id":"req-3b","timestamp":"%s","params":{"model":"claude-opus-5-5"}}`+"\n",
		t4.Format(time.RFC3339), t4.Add(time.Second).Format(time.RFC3339),
	)
	if err := os.WriteFile(sessionFile, []byte(part5), 0o644); err != nil {
		t.Fatal(err)
	}
	idx.ScanRoots([]string{tmp}, nil)
	if pend := idx.SpeedLog().Pending; len(pend) != 1 || pend[0].ID != "req-3b" {
		t.Fatalf("expected single latest pending req-3b for same file, got %+v", pend)
	}

	indexPath := filepath.Join(tmp, "index.gob")
	if err := idx.Save(indexPath); err != nil {
		t.Fatal(err)
	}
	reloaded := LoadUsageIndex(indexPath)
	if len(reloaded.Pending) != 0 {
		t.Fatalf("expected empty pending before first scan after reload, got %+v", reloaded.Pending)
	}
	reloaded.ScanRoots([]string{tmp}, nil)
	if pend := reloaded.SpeedLog().Pending; len(pend) != 1 || pend[0].ID != "req-3b" {
		t.Fatalf("expected startup priming to recover in-flight pending req-3b, got %+v", pend)
	}
	if len(reloaded.Records) != 1 {
		t.Fatalf("startup priming must not duplicate records, got %d", len(reloaded.Records))
	}
}

func TestPillPreferencesPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.json")

	// Missing pillAlwaysOnTop in legacy JSON must default to true.
	if err := os.WriteFile(path, []byte(`{"showFloatingPill":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p := LoadPreferences(path)
	if !p.PillAlwaysOnTop || p.PillPosX != nil || p.PillPosY != nil {
		t.Fatalf("unexpected legacy pill defaults: %+v", p)
	}

	// Explicit false and (0, 0) coordinates must persist cleanly.
	x, y := 0, 0
	p.PillAlwaysOnTop = false
	p.PillPosX = &x
	p.PillPosY = &y
	if err := SavePreferences(path, p); err != nil {
		t.Fatal(err)
	}
	p2 := LoadPreferences(path)
	if p2.PillAlwaysOnTop || p2.PillPosX == nil || *p2.PillPosX != 0 || p2.PillPosY == nil || *p2.PillPosY != 0 {
		t.Fatalf("unexpected reloaded pill prefs: %+v", p2)
	}
}

func TestPlanKindTitlesAndSubscriptionJustStarted(t *testing.T) {
	if PlanPro.Title() != "Pro" {
		t.Fatalf("expected Pro, got %q", PlanPro.Title())
	}
	if PlanMax5x.Title() != "Max" {
		t.Fatalf("expected Max for PlanMax5x, got %q", PlanMax5x.Title())
	}
	if PlanMax20x.Title() != "Ultra" {
		t.Fatalf("expected Ultra for PlanMax20x, got %q", PlanMax20x.Title())
	}
	if PlanUltra.Title() != "Ultra" {
		t.Fatalf("expected Ultra for PlanUltra, got %q", PlanUltra.Title())
	}

	start := time.Date(2026, 11, 20, 0, 0, 0, 0, time.Local)
	term := SubscriptionTerm{Start: start, End: start.AddDate(0, 1, 0), DaysLeft: 30}
	if !term.JustStarted(start.Add(12 * time.Hour)) {
		t.Fatal("expected JustStarted=true within 24h of start")
	}
	if term.JustStarted(start.Add(25 * time.Hour)) {
		t.Fatal("expected JustStarted=false after 24h")
	}
}

func TestFrankfurterFXRatesAndMoneyFormatting(t *testing.T) {
	rates, err := ParseFrankfurterRates([]byte(`{"amount":1.0,"base":"USD","date":"2026-11-24","rates":{"CNY":7.12,"JPY":152.5,"EUR":0.92,"BAD":-1}}`))
	if err != nil {
		t.Fatalf("ParseFrankfurterRates failed: %v", err)
	}
	if rates["USD"] != 1 || rates["CNY"] != 7.12 || rates["JPY"] != 152.5 {
		t.Fatalf("unexpected parsed rates: %+v", rates)
	}
	if _, ok := rates["BAD"]; ok {
		t.Fatal("non-positive rate must be ignored")
	}
	if _, err := ParseFrankfurterRates([]byte(`{"rates":{}}`)); err == nil {
		t.Fatal("expected error on empty rates payload")
	}

	cny := NewMoneyFormatWithRates(CurrencyCNY, rates)
	if got := cny.Format(10); got != "¥71.20" {
		t.Fatalf("expected live CNY ¥71.20, got %q", got)
	}
	// Large values (>= 10000 in display currency) drop decimals cleanly.
	if got := cny.Format(2000); got != "¥14,240" {
		t.Fatalf("expected ¥14,240 without decimals, got %q", got)
	}
	// Fallback rate when currency not in map.
	twd := NewMoneyFormatWithRates(CurrencyTWD, rates)
	if twd.Rate != CurrencyTWD.DefaultRate() {
		t.Fatalf("expected fallback TWD rate %v, got %v", CurrencyTWD.DefaultRate(), twd.Rate)
	}
	// JPY uses JP¥ and 0 decimal places.
	jpy := NewMoneyFormatWithRates(CurrencyJPY, rates)
	if got := jpy.Format(10); got != "JP¥1,525" {
		t.Fatalf("expected JP¥1,525, got %q", got)
	}
	// Whole rounds >= 100 to integer, keeps 2 decimals below 100.
	usd := NewMoneyFormatWithRates(CurrencyUSD, nil)
	if got := usd.Whole(42.5); got != "$42.50" {
		t.Fatalf("expected $42.50, got %q", got)
	}
	if got := usd.Whole(100.4); got != "$100" {
		t.Fatalf("expected $100, got %q", got)
	}
}

func TestComputeSpeedRateMedianResistance(t *testing.T) {
	now := 1000.0
	requests := []TimedRequest{
		{ID: 1, Model: "claude-opus-5-5", Start: 800, End: 900, Output: 100},  // stalled outlier: 1 tok/s
		{ID: 2, Model: "claude-opus-5-5", Start: 910, End: 910.2, Output: 20}, // short reply: 100 tok/s, <100 tokens -> ignored in rank
		{ID: 3, Model: "claude-opus-5-5", Start: 920, End: 930, Output: 800},  // 80 tok/s
		{ID: 4, Model: "claude-opus-5-5", Start: 940, End: 950, Output: 900},  // 90 tok/s
		{ID: 5, Model: "claude-opus-5-5", Start: 960, End: 970, Output: 1000}, // 100 tok/s
	}
	rate := ComputeSpeedRate(requests, now-300, now)
	if rate == nil {
		t.Fatal("expected non-nil SpeedRate")
	}
	// Weighted mean would be ~21.7 tok/s due to the 100s stalled outlier; median of {1, 80, 90, 100} is (80+90)/2 = 85.0!
	if math.Abs(rate.TokensPerSecond()-85.0) > 1e-6 {
		t.Fatalf("expected median speed 85.0 tok/s, got %v (rate=%+v)", rate.TokensPerSecond(), rate)
	}
}
