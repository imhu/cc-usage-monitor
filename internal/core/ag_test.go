package core

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAGTranscriptParserAndBackfill(t *testing.T) {
	jsonl := "" +
		`{"step_index":0,"type":"USER_INPUT","created_at":"2026-11-23T06:35:24Z","content":"hi"}` + "\n" +
		`{broken json line` + "\n" +
		`{"step_index":1,"type":"PLANNER_RESPONSE","created_at":"2026-11-23T06:35:45Z","input_tokens":14611,"cache_read_tokens":0,"output_tokens":95}` + "\n" +
		`{"step_index":2,"type":"PLANNER_RESPONSE","created_at":"2026-11-23T06:35:49Z","model":"gemini-4-neon-xhigh","input_tokens":7677,"cache_read_tokens":22582,"output_tokens":587}` + "\n" +
		`{"step_index":3,"type":"PLANNER_RESPONSE","created_at":"2026-11-23T06:36:10.500Z","model":"gemini-4-hydrogen-tiered-high","input_tokens":5000,"cache_read_tokens":30000,"output_tokens":500}` + "\n"

	recs := ParseAGTranscriptBytes([]byte(jsonl), "conv-1", nil)
	if len(recs) != 3 {
		t.Fatalf("expected 3 parsed records, got %d", len(recs))
	}
	// Leading model-less record is backfilled once real model appears.
	if recs[0].Model != "gemini-4-neon-xhigh" {
		t.Fatalf("expected backfilled model gemini-4-neon-xhigh, got %q", recs[0].Model)
	}
	if recs[1].TotalInputTokens() != 30259 {
		t.Fatalf("expected total input 30259, got %d", recs[1].TotalInputTokens())
	}
	if recs[2].Model != "gemini-4-hydrogen-tiered-high" {
		t.Fatalf("expected model gemini-4-hydrogen-tiered-high, got %q", recs[2].Model)
	}
}

func TestAGTranscriptIncrementalIndex(t *testing.T) {
	root := t.TempDir()
	logsDir := filepath.Join(root, "conv-a", ".system_generated", "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(logsDir, "transcript.jsonl")
	line1 := `{"created_at":"2026-11-23T06:00:00Z","model":"gemini-2.5-flash","input_tokens":1000,"cache_read_tokens":4000,"output_tokens":200}` + "\n"
	if err := os.WriteFile(filePath, []byte(line1), 0o644); err != nil {
		t.Fatal(err)
	}

	idx := NewAGTranscriptIndex(root)
	recs, convCount := idx.Refresh()
	if len(recs) != 1 || convCount != 1 {
		t.Fatalf("pass1: expected 1 record & 1 conv, got %d recs, %d convs", len(recs), convCount)
	}

	// Append one complete line + one partial trailing line without newline.
	f, err := os.OpenFile(filePath, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	line2 := `{"created_at":"2026-11-23T06:10:00Z","model":"gemini-2.5-flash","input_tokens":500,"cache_read_tokens":4500,"output_tokens":150}` + "\n"
	partial := `{"created_at":"2026-11-23T06:11:00Z","model":"gemini-2.5-fla`
	if _, err := f.WriteString(line2 + partial); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	recs, _ = idx.Refresh()
	if len(recs) != 2 || recs[1].CacheReadTokens != 4500 {
		t.Fatalf("pass2: expected 2 records with cacheRead=4500, got %+v", recs)
	}

	// Finish the partial line and verify it gets picked up on next Refresh.
	f, err = os.OpenFile(filePath, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`sh","input_tokens":100,"cache_read_tokens":0,"output_tokens":50}` + "\n"); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	recs, _ = idx.Refresh()
	if len(recs) != 3 || recs[2].OutputTokens != 50 {
		t.Fatalf("pass3: expected 3 records after partial completed, got %+v", recs)
	}

	// Truncation/rewrite must re-parse cleanly from offset 0.
	lineReset := `{"created_at":"2026-11-23T07:00:00Z","model":"gemini-4-neon-xhigh","input_tokens":42,"cache_read_tokens":0,"output_tokens":7}` + "\n"
	if err := os.WriteFile(filePath, []byte(lineReset), 0o644); err != nil {
		t.Fatal(err)
	}
	recs, _ = idx.Refresh()
	if len(recs) != 1 || recs[0].InputTokens != 42 {
		t.Fatalf("pass4 truncation: expected 1 record input=42, got %+v", recs)
	}
}

func TestAGPricingAndDisplayName(t *testing.T) {
	p := AGPriceFor("gemini-4-neon-xhigh")
	if p == nil {
		t.Fatal("expected price for gemini-4-neon-xhigh")
	}
	cost := p.CostUSD(1_000_000, 1_000_000, 1_000_000)
	if math.Abs(cost-18.15) > 1e-6 {
		t.Fatalf("expected 18.15 USD, got %v", cost)
	}
	saved := p.CacheSavingsUSD(1_000_000)
	if math.Abs(saved-2.85) > 1e-6 {
		t.Fatalf("expected 2.85 savings, got %v", saved)
	}
	if AGPriceFor("experimental-mystery-9b") != nil {
		t.Fatal("expected nil price for unknown model (never fabricate)")
	}
	if got := AGModelDisplayName("gpt-4o-mini"); got != "GPT 4o Mini" {
		t.Fatalf("unexpected display name: %q", got)
	}
}

func TestAGQuotaEvaluationAndResetZero(t *testing.T) {
	now, _ := ParseAGISO8601("2026-11-23T12:00:00Z")
	capturedAt := now.Add(-45 * time.Minute)
	rem5h := 0.38
	remWk := 0.80
	usedCtx := 9.0
	ctxSize := int64(1_000_000)
	totIn := int64(80_000)
	totOut := int64(10_000)
	rSec5h := 9000
	rSecWk := 5 * 86400
	costUSD := 0.42

	snap := &AGStatusLineSnapshot{
		ConversationID: "conv-1",
		Model:          &AGStatusLineModelInfo{ID: "gemini-2.5-pro", DisplayName: "Gemini 2.5 Pro"},
		ContextWindow: &AGStatusLineContextWindow{
			TotalInputTokens:  &totIn,
			TotalOutputTokens: &totOut,
			ContextWindowSize: &ctxSize,
			UsedPercentage:    &usedCtx,
		},
		Quota: map[string]AGStatusLineQuotaBucket{
			"gemini-5h":     {RemainingFraction: &rem5h, ResetTime: "2026-11-23T14:30:00Z", ResetInSeconds: &rSec5h},
			"gemini-weekly": {RemainingFraction: &remWk, ResetTime: "2026-11-28T12:00:00Z", ResetInSeconds: &rSecWk},
		},
		PlanTier:   "Pro",
		Email:      "dev@example.com",
		Cost:       &AGStatusLineCostInfo{TotalUSD: &costUSD},
		CapturedAt: &AGFlexibleTime{T: &capturedAt},
	}

	report := EvaluateAGQuota(snap, now)
	if !report.HasSnapshot() || !report.IsStale {
		t.Fatalf("expected stale snapshot, got has=%v stale=%v", report.HasSnapshot(), report.IsStale)
	}
	if report.PrimaryFiveHour == nil || report.PrimaryFiveHour.UsedPercentInt() != 62 || !report.PrimaryFiveHour.IsOverPace() {
		t.Fatalf("expected 5h 62%% over pace, got %+v", report.PrimaryFiveHour)
	}
	if math.Abs(*report.PrimaryFiveHour.SafeLineFraction-0.5) > 0.01 {
		t.Fatalf("expected 5h safe line ~0.5, got %v", *report.PrimaryFiveHour.SafeLineFraction)
	}
	if report.PrimaryWeekly == nil || report.PrimaryWeekly.UsedPercentInt() != 20 || report.PrimaryWeekly.IsOverPace() {
		t.Fatalf("expected weekly 20%% safe, got %+v", report.PrimaryWeekly)
	}
	if math.Abs(*report.PrimaryWeekly.SafeLineFraction-3.0/7.0) > 0.01 {
		t.Fatalf("expected weekly safe line ~3/7, got %v", *report.PrimaryWeekly.SafeLineFraction)
	}

	// Reset elapsed drops local usage to 0
	after := now.Add(3 * time.Hour)
	resetRep := EvaluateAGQuota(snap, after)
	if resetRep.PrimaryFiveHour == nil || !resetRep.PrimaryFiveHour.IsResetElapsed || resetRep.PrimaryFiveHour.UsedPercentInt() != 0 || resetRep.PrimaryFiveHour.IsOverPace() {
		t.Fatalf("expected 5h reset elapsed -> 0%%, got %+v", resetRep.PrimaryFiveHour)
	}

	// Unix ms fallback in _captured_at
	rawJSON := `{"_captured_at":1763899200000,"quota":{"3p-5h":{"remaining_fraction":0.5,"reset_in_seconds":3600}}}`
	var decoded AGStatusLineSnapshot
	if err := json.Unmarshal([]byte(rawJSON), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.CapturedTime() == nil || decoded.CapturedTime().Unix() != 1763899200 {
		t.Fatalf("expected unix ms decoded to 1763899200, got %+v", decoded.CapturedTime())
	}

	// Inactive bucket with Unix epoch 1970-01-01 must NOT be marked reset elapsed
	rSecZero := 0
	remOne := 1.0
	inactiveSnap := &AGStatusLineSnapshot{
		Quota: map[string]AGStatusLineQuotaBucket{
			"gemini-5h": {RemainingFraction: &remOne, ResetTime: "1970-01-01T00:00:00Z", ResetInSeconds: &rSecZero},
		},
		CapturedAt: &AGFlexibleTime{T: &now},
	}
	inactiveRep := EvaluateAGQuota(inactiveSnap, now)
	if inactiveRep.PrimaryFiveHour == nil || inactiveRep.PrimaryFiveHour.ResetTime != nil ||
		inactiveRep.PrimaryFiveHour.IsResetElapsed || inactiveRep.PrimaryFiveHour.RemainingPercentInt() != 100 {
		t.Fatalf("unexpected inactive bucket eval: %+v", inactiveRep.PrimaryFiveHour)
	}

	// Integer percentage self-consistency: 40.2% vs safe 40.1% -> both 40% => not over pace
	safe40 := 0.401
	bSelf := AGQuotaBucketEval{UsedFraction: 0.402, SafeLineFraction: &safe40}
	if bSelf.UsedPercentInt() != 40 || *bSelf.SafeLinePercentInt() != 40 || bSelf.IsOverPace() {
		t.Fatalf("expected 40%% vs 40%% not over pace, got used=%d safe=%d over=%v",
			bSelf.UsedPercentInt(), *bSelf.SafeLinePercentInt(), bSelf.IsOverPace())
	}

	// 5-hour settling buffer: first 10% (30 min) does not trigger over-pace warning
	remSettling := 0.985
	rSecSettling := 17940
	settlingSnap := &AGStatusLineSnapshot{
		Quota: map[string]AGStatusLineQuotaBucket{
			"gemini-5h": {
				RemainingFraction: &remSettling,
				ResetTime:         now.Add(5*time.Hour - time.Minute).UTC().Format(time.RFC3339),
				ResetInSeconds:    &rSecSettling,
			},
		},
		CapturedAt: &AGFlexibleTime{T: &now},
	}
	settlingRep := EvaluateAGQuota(settlingSnap, now)
	if settlingRep.PrimaryFiveHour == nil || settlingRep.PrimaryFiveHour.UsedPercentInt() != 1 ||
		!settlingRep.PrimaryFiveHour.IsPaceSettling || settlingRep.PrimaryFiveHour.IsOverPace() {
		t.Fatalf("expected 1%% in settling window not over pace, got %+v", settlingRep.PrimaryFiveHour)
	}
}

func TestAGAggregatorAndSpeedEstimation(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.Local)
	records := []AGTranscriptRecord{
		{Timestamp: now.Add(-15 * time.Minute), Model: "gemini-2.5-pro", InputTokens: 100_000, CacheReadTokens: 900_000, OutputTokens: 10_000, ConversationID: "c1"},
		{Timestamp: now.Add(-14*time.Minute - 40*time.Second), Model: "gemini-2.5-pro", InputTokens: 1_000, CacheReadTokens: 0, OutputTokens: 1_200, ConversationID: "c1"}, // dt = 20s -> 60 tok/s
		{Timestamp: now.Add(-10 * time.Minute), Model: "experimental-mystery-9b", InputTokens: 50_000, CacheReadTokens: 50_000, OutputTokens: 5_000, ConversationID: "c2"},
		{Timestamp: now.Add(-48 * time.Hour), Model: "claude-sonnet-4", InputTokens: 200_000, CacheReadTokens: 800_000, OutputTokens: 20_000, ConversationID: "c3"},
	}

	report := AggregateAGUsage(records, 3, now, nil, 15)
	if report.Today.UncachedInputTokens != 151_000 || report.Today.CacheReadTokens != 950_000 || report.Today.OutputTokens != 16_200 {
		t.Fatalf("unexpected today totals: %+v", report.Today)
	}
	if !report.Today.HasUnpricedModels() || len(report.Today.UnpricedModels) != 1 || report.Today.UnpricedModels[0] != "experimental-mystery-9b" {
		t.Fatalf("expected experimental-mystery-9b unpriced, got %+v", report.Today.UnpricedModels)
	}
	if report.Week.RequestCount != 4 || len(report.DailyTrend) != 14 {
		t.Fatalf("expected 4 weekly reqs & 14-day trend, got %d / %d", report.Week.RequestCount, len(report.DailyTrend))
	}
	if report.AllTime.RequestCount != 4 || report.AllTime.TotalTokens() < report.Week.TotalTokens() || report.EarliestRecordDate == nil {
		t.Fatalf("unexpected AllTime summary: %+v earliest=%v", report.AllTime, report.EarliestRecordDate)
	}
	if report.MonthPeriod == nil {
		t.Fatal("expected MonthPeriod when billingDay=15")
	}
	if report.OutputSpeed.DisplayTokPerSec() == nil || len(report.OutputSpeed.Samples) == 0 {
		t.Fatalf("expected non-empty estimated output speed, got %+v", report.OutputSpeed)
	}
}

func TestAGBridgeInstallerDirectAndChained(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"verbosity":"medium"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	inst := AGBridgeInstaller{CLIConfigDir: dir, CaptureExePath: `C:\Program Files\ccmonitor\ccmonitor.exe`}
	if st := inst.Status(); st.IsInstalled() {
		t.Fatalf("expected not installed initially, got %+v", st)
	}

	st, err := inst.Install()
	if err != nil {
		t.Fatalf("direct install failed: %v", err)
	}
	if st.Kind != AGBridgeInstalledDirect || !inst.Status().IsInstalled() {
		t.Fatalf("expected direct installed status, got %+v / %+v", st, inst.Status())
	}
	if _, err := os.Stat(inst.BackupPath()); err != nil {
		t.Fatalf("expected backup file: %v", err)
	}
	rawInstalled, _ := os.ReadFile(settingsPath)
	var installedCfg map[string]any
	_ = json.Unmarshal(rawInstalled, &installedCfg)
	installedSL, _ := installedCfg["statusLine"].(map[string]any)
	installedCmd, _ := installedSL["command"].(string)
	if installedCmd != inst.CaptureScriptPath() || strings.Contains(installedCmd, `"`) {
		t.Fatalf("expected quote-free capture script path %q, got %q", inst.CaptureScriptPath(), installedCmd)
	}
	scriptBytes, err := os.ReadFile(inst.CaptureScriptPath())
	if err != nil {
		t.Fatalf("expected capture script %q: %v", inst.CaptureScriptPath(), err)
	}
	if !strings.Contains(string(scriptBytes), `C:\Program Files\ccmonitor\ccmonitor.exe`) {
		t.Fatalf("expected capture script to contain executable path, got:\n%s", string(scriptBytes))
	}

	if err := inst.Uninstall(); err != nil {
		t.Fatalf("direct uninstall failed: %v", err)
	}
	if inst.Status().IsInstalled() {
		t.Fatal("expected uninstalled after Uninstall")
	}
	if _, err := os.Stat(inst.CaptureScriptPath()); !os.IsNotExist(err) {
		t.Fatalf("expected capture script to be removed on uninstall, stat err=%v", err)
	}
	raw, _ := os.ReadFile(settingsPath)
	var restored map[string]any
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if restored["verbosity"] != "medium" || restored["statusLine"] != nil {
		t.Fatalf("unexpected restored settings after direct uninstall: %+v", restored)
	}

	// Chained install & restore
	orig := map[string]any{
		"statusLine": map[string]any{
			"type":               "command",
			"command":            `C:\tools\custom.cmd`,
			"padding":            1,
			"stack_with_default": false,
		},
	}
	b, _ := json.MarshalIndent(orig, "", "  ")
	if err := os.WriteFile(settingsPath, b, 0o644); err != nil {
		t.Fatal(err)
	}

	st, err = inst.Install()
	if err != nil || st.Kind != AGBridgeInstalledChained || st.OriginalCommand != `C:\tools\custom.cmd` {
		t.Fatalf("expected chained install, got %+v err=%v", st, err)
	}
	// Idempotent reinstall preserves original command
	st2, err := inst.Install()
	if err != nil || st2.Kind != AGBridgeInstalledChained || st2.OriginalCommand != `C:\tools\custom.cmd` {
		t.Fatalf("expected idempotent chained install, got %+v err=%v", st2, err)
	}
	if err := inst.Uninstall(); err != nil {
		t.Fatalf("chained uninstall failed: %v", err)
	}
	raw2, _ := os.ReadFile(settingsPath)
	var restored2 map[string]any
	_ = json.Unmarshal(raw2, &restored2)
	sl, _ := restored2["statusLine"].(map[string]any)
	if sl["command"] != `C:\tools\custom.cmd` || sl["stack_with_default"] != false {
		t.Fatalf("expected original statusLine restored, got %+v", sl)
	}
}

func TestAGContextWindowSizeAndFallbackResolution(t *testing.T) {
	if got := AGContextWindowSizeFor("gemini-4-neon-high"); got != 1_048_576 {
		t.Fatalf("gemini window = %d, want 1048576", got)
	}
	if got := AGContextWindowSizeFor("claude-sonnet-4-5"); got != 200_000 {
		t.Fatalf("claude 200k window = %d, want 200000", got)
	}
	if got := AGContextWindowSizeFor("claude-sonnet-4-6-1m"); got != 1_000_000 {
		t.Fatalf("claude 1m window = %d, want 1000000", got)
	}
	if got := AGContextWindowSizeFor("gpt-5"); got != 400_000 {
		t.Fatalf("gpt-5 window = %d, want 400000", got)
	}
	if got := AGContextWindowSizeFor("gpt-4o-mini"); got != 128_000 {
		t.Fatalf("gpt-4o window = %d, want 128000", got)
	}

	now := time.Date(2026, 11, 24, 14, 0, 0, 0, time.UTC)

	stale := AggregateAGUsage([]AGTranscriptRecord{
		{Timestamp: now.Add(-7 * time.Hour), Model: "gemini-4-neon-high", InputTokens: 5000, CacheReadTokens: 200000, OutputTokens: 500, ConversationID: "old"},
	}, 1, now, nil, 0)
	if stale.LatestContext != nil {
		t.Fatalf("expected nil LatestContext for >6h stale turn, got %+v", stale.LatestContext)
	}

	active := AggregateAGUsage([]AGTranscriptRecord{
		{Timestamp: now.Add(-20 * time.Minute), Model: "claude-sonnet-4-5", InputTokens: 1000, CacheReadTokens: 49000, OutputTokens: 0, ConversationID: "c1"},
		{Timestamp: now.Add(-2 * time.Minute), Model: "gemini-4-neon-high", InputTokens: 2802, CacheReadTokens: 290350, OutputTokens: 1021, ConversationID: "c2"},
	}, 2, now, nil, 0)
	if active.LatestContext == nil || active.LatestContext.ConversationID != "c2" || active.LatestContext.UsedTokens != 294173 {
		t.Fatalf("unexpected LatestContext: %+v", active.LatestContext)
	}
	if math.Abs(active.LatestContext.UsedPercentage-(294173.0/1048576.0*100.0)) > 1e-6 {
		t.Fatalf("unexpected UsedPercentage: %v", active.LatestContext.UsedPercentage)
	}

	zeroPct := 0.0
	zeroTok := int64(0)
	winSize := int64(1_048_576)
	emptyOff := AGOfficialQuotaReport{
		ContextWindow: &AGStatusLineContextWindow{
			TotalInputTokens:  &zeroTok,
			TotalOutputTokens: &zeroTok,
			ContextWindowSize: &winSize,
			UsedPercentage:    &zeroPct,
		},
	}
	pct, used, size, ok := ResolveAGContextWindow(emptyOff, active)
	if !ok || used != 294173 || size != 1_048_576 || math.Abs(pct-active.LatestContext.UsedPercentage) > 1e-6 {
		t.Fatalf("expected fallback to local LatestContext, got ok=%v pct=%v used=%d size=%d", ok, pct, used, size)
	}

	offPct := 61.5
	offIn := int64(600000)
	offOut := int64(45000)
	realOff := AGOfficialQuotaReport{
		ContextWindow: &AGStatusLineContextWindow{
			TotalInputTokens:  &offIn,
			TotalOutputTokens: &offOut,
			ContextWindowSize: &winSize,
			UsedPercentage:    &offPct,
		},
	}
	pct, used, size, ok = ResolveAGContextWindow(realOff, active)
	if !ok || pct != 61.5 || used != 645000 || size != 1_048_576 {
		t.Fatalf("expected official priority, got ok=%v pct=%v used=%d size=%d", ok, pct, used, size)
	}

	if pct, used, size, ok := ResolveAGContextWindow(emptyOff, stale); ok || pct != 0 || used != 0 || size != 0 {
		t.Fatalf("expected ok=false when no active context, got ok=%v pct=%v used=%d size=%d", ok, pct, used, size)
	}
}

func pbVarintField(num int, v uint64) []byte {
	var buf [32]byte
	n := binary.PutUvarint(buf[:], uint64(num<<3))
	n += binary.PutUvarint(buf[n:], v)
	return buf[:n]
}

func pbBytesField(num int, payload []byte) []byte {
	var buf [32]byte
	n := binary.PutUvarint(buf[:], uint64((num<<3)|2))
	n += binary.PutUvarint(buf[n:], uint64(len(payload)))
	out := make([]byte, 0, n+len(payload))
	out = append(out, buf[:n]...)
	out = append(out, payload...)
	return out
}

func pbTimestampBlob(t time.Time) []byte {
	if t.IsZero() {
		return nil
	}
	var out []byte
	out = append(out, pbVarintField(1, uint64(t.Unix()))...)
	if ns := t.Nanosecond(); ns > 0 {
		out = append(out, pbVarintField(2, uint64(ns))...)
	}
	return out
}

func cortexStepMetaBlob(start, first, end time.Time, respID string) []byte {
	var out []byte
	if !start.IsZero() {
		out = append(out, pbBytesField(1, pbTimestampBlob(start))...)
	}
	if !first.IsZero() {
		out = append(out, pbBytesField(6, pbTimestampBlob(first))...)
	}
	if !end.IsZero() {
		out = append(out, pbBytesField(7, pbTimestampBlob(end))...)
	}
	if respID != "" {
		out = append(out, pbBytesField(9, pbBytesField(11, []byte(respID)))...)
	}
	return out
}

func TestReadAGConversationStepTimingsAndExactOutputSpeed(t *testing.T) {
	root := t.TempDir()
	convID := "conv-exact-speed"
	brainLogsDir := filepath.Join(root, "brain", convID, ".system_generated", "logs")
	convDir := filepath.Join(root, "conversations")
	if err := os.MkdirAll(brainLogsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(convDir, convID+".db")
	db, err := sql.Open("sqlite", sqliteReadOnlyURI(dbPath))
	_ = db
	// Create writable DB first
	wdb, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wdb.Exec(`CREATE TABLE steps (idx INTEGER PRIMARY KEY, metadata BLOB)`); err != nil {
		wdb.Close()
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	// Step 1 & 2 share response_id "resp-shared": step 1 had start & first content, step 2 finished decoding.
	// Step 1: start = now-4m, first = now-4m+2s -> TTFT = 2000ms
	// Step 2: start = now-4m+3s, end = now-4m+6s -> merged start = now-4m, first = now-4m+2s, end = now-4m+6s => TTFT=2000ms, Decode=4000ms, Output=400 => 100 tok/s
	t1 := now.Add(-4 * time.Minute)
	// Step 3: TTFT=1500ms, Decode=2000ms, Output=240 => 120 tok/s
	t3 := now.Add(-3 * time.Minute)
	// Step 4: TTFT=1500ms, Decode=2000ms, Output=280 => 140 tok/s
	t4 := now.Add(-2 * time.Minute)
	// Step 5 (held burst): TTFT=8000ms, Decode=200ms (<1000ms and 8000 > 4*200) -> MUST be filtered out!
	t5 := now.Add(-90 * time.Second)
	// Step 6 (stalled outlier): TTFT=1000ms, Decode=10000ms, Output=100 => 10 tok/s -> median stays robust against 10 tok/s!
	t6 := now.Add(-60 * time.Second)
	// Step 7 (short noise <50 tokens): TTFT=500ms, Decode=2000ms, Output=20 => 10 tok/s -> ignored in ranked median
	t7 := now.Add(-30 * time.Second)

	inserts := []struct {
		idx  int
		blob []byte
	}{
		{1, cortexStepMetaBlob(t1, t1.Add(2*time.Second), t1.Add(3*time.Second), "resp-shared")},
		{2, cortexStepMetaBlob(t1.Add(3*time.Second), time.Time{}, t1.Add(6*time.Second), "resp-shared")},
		{3, cortexStepMetaBlob(t3, t3.Add(1500*time.Millisecond), t3.Add(3500*time.Millisecond), "resp-3")},
		{4, cortexStepMetaBlob(t4, t4.Add(1500*time.Millisecond), t4.Add(3500*time.Millisecond), "resp-4")},
		{5, cortexStepMetaBlob(t5, t5.Add(8*time.Second), t5.Add(8200*time.Millisecond), "resp-5")},
		{6, cortexStepMetaBlob(t6, t6.Add(1*time.Second), t6.Add(11*time.Second), "resp-6")},
		{7, cortexStepMetaBlob(t7, t7.Add(500*time.Millisecond), t7.Add(2500*time.Millisecond), "resp-7")},
	}
	for _, ins := range inserts {
		if _, err := wdb.Exec(`INSERT INTO steps (idx, metadata) VALUES (?, ?)`, ins.idx, ins.blob); err != nil {
			wdb.Close()
			t.Fatal(err)
		}
	}
	wdb.Close()

	timings, err := ReadAGConversationStepTimings(dbPath)
	if err != nil {
		t.Fatalf("ReadAGConversationStepTimings failed: %v", err)
	}
	tm2, ok := timings[2]
	if !ok || tm2.TTFTMs != 2000 || tm2.DecodeMs != 4000 || tm2.DurationMs != 6000 {
		t.Fatalf("expected merged step 2 TTFT=2000 Decode=4000 Duration=6000, got %+v (ok=%v)", tm2, ok)
	}

	transcript := "" +
		fmt.Sprintf(`{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"%s","model":"gemini-4-neon-high","input_tokens":500,"cache_read_tokens":9500,"output_tokens":400}`+"\n", t1.Add(6*time.Second).Format(time.RFC3339)) +
		fmt.Sprintf(`{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"%s","model":"gemini-4-neon-high","input_tokens":500,"cache_read_tokens":9500,"output_tokens":240}`+"\n", t3.Add(3500*time.Millisecond).Format(time.RFC3339)) +
		fmt.Sprintf(`{"step_index":4,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"%s","model":"gemini-4-neon-high","input_tokens":500,"cache_read_tokens":9500,"output_tokens":280}`+"\n", t4.Add(3500*time.Millisecond).Format(time.RFC3339)) +
		fmt.Sprintf(`{"step_index":5,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"%s","model":"gemini-4-neon-high","input_tokens":500,"cache_read_tokens":9500,"output_tokens":500}`+"\n", t5.Add(8200*time.Millisecond).Format(time.RFC3339)) +
		fmt.Sprintf(`{"step_index":6,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"%s","model":"gemini-4-neon-high","input_tokens":500,"cache_read_tokens":9500,"output_tokens":100}`+"\n", t6.Add(11*time.Second).Format(time.RFC3339)) +
		fmt.Sprintf(`{"step_index":7,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"%s","model":"gemini-4-neon-high","input_tokens":500,"cache_read_tokens":9500,"output_tokens":20}`+"\n", t7.Add(2500*time.Millisecond).Format(time.RFC3339))

	if err := os.WriteFile(filepath.Join(brainLogsDir, "transcript.jsonl"), []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}

	idx := NewAGTranscriptIndex(filepath.Join(root, "brain"))
	recs, convCount := idx.Refresh()
	if convCount != 1 || len(recs) != 6 {
		t.Fatalf("expected 1 conv / 6 recs, got %d / %d", convCount, len(recs))
	}
	if recs[0].DecodeMs != 4000 || recs[0].TTFTMs != 2000 {
		t.Fatalf("expected record 0 enriched with DB timing, got %+v", recs[0])
	}

	evalNow := t7.Add(5 * time.Second)
	speed := EstimateAGOutputSpeed(recs, evalNow)
	if !speed.IsExact {
		t.Fatalf("expected IsExact=true, got %+v", speed)
	}
	// Step 5 filtered (held burst); remaining samples = steps {2, 3, 4, 6, 7};
	// ranked (>=50 tokens) = {10, 100, 120, 140} -> median = (100+120)/2 = 110.0!
	if speed.Recent5mTokPerSec == nil || math.Abs(*speed.Recent5mTokPerSec-110.0) > 1e-6 {
		t.Fatalf("expected exact 5m median speed 110.0 tok/s, got %v (samples=%+v)", speed.Recent5mTokPerSec, speed.Samples)
	}
	if speed.Recent1hTokPerSec == nil || math.Abs(*speed.Recent1hTokPerSec-110.0) > 1e-6 {
		t.Fatalf("expected exact 1h median speed 110.0 tok/s, got %v", speed.Recent1hTokPerSec)
	}
	// TTFTs for all 5 kept samples = {500, 1000, 1500, 1500, 2000} -> median = 1500ms!
	if speed.MedianTTFTMs == nil || *speed.MedianTTFTMs != 1500 {
		t.Fatalf("expected MedianTTFTMs=1500, got %v", speed.MedianTTFTMs)
	}
}
