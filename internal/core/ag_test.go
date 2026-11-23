package core

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
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

	if err := inst.Uninstall(); err != nil {
		t.Fatalf("direct uninstall failed: %v", err)
	}
	if inst.Status().IsInstalled() {
		t.Fatal("expected uninstalled after Uninstall")
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
