package service

import (
	"time"

	"ccmonitor/internal/core"
	"ccmonitor/internal/platform"
)

// SetDemoState populates the store with sample data matching the reference screenshot,
// useful for visual verification tests and `--demo` preview mode.
func (s *Store) SetDemoState() {
	now := time.Now()
	nowSec := float64(now.UnixNano()) / 1e9
	reset5h := now.Add(2*time.Hour + 30*time.Minute)
	reset7d := now.Add(6*24*time.Hour + 12*time.Hour)
	syncedAt := now.Add(-1 * time.Minute)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.phase = PhaseReady
	s.officialState = OfficialConnected
	s.officialUsage = &core.OfficialUsage{
		FiveHour: &core.OfficialLimit{Utilization: 8.4, ResetsAt: &reset5h},
		SevenDay: &core.OfficialLimit{Utilization: 10.2, ResetsAt: &reset7d},
		Scoped: []core.ScopedLimit{
			{ModelName: "Fable", Limit: core.OfficialLimit{Utilization: 0, ResetsAt: &reset7d}},
		},
		FetchedAt: syncedAt,
	}
	s.profile = &core.AccountProfile{
		OrganizationType:   "claude_max",
		RateLimitTier:      "default_claude_max_20x",
		SubscriptionStatus: "active",
		Email:              "demo@example.com",
		AccountUUID:        "demo-uuid",
	}
	s.exitPlace = &core.PublicNetwork{
		IP:          "162.141.213.48",
		CountryCode: "US",
		Region:      "SJC",
	}
	s.exitFailed = false
	s.exitChecking = false
	s.processes = []platform.ClaudeTask{
		{PID: 4210, Name: "Claude Code", Detail: "ccmonitor", Bytes: 186 * 1024 * 1024},
	}

	// Build 14 days of realistic daily bars
	dailyCosts := []float64{42, 118, 86, 65, 92, 68, 128, 82, 160, 108, 2, 2, 2, 280}
	daily := make([]core.DayUsage, len(dailyCosts))
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for i, c := range dailyCosts {
		daily[i] = core.DayUsage{
			Date:     today.AddDate(0, 0, i-(len(dailyCosts)-1)),
			Cost:     c,
			Requests: int(c * 8),
		}
	}

	wStart := reset7d.Add(-core.WeekWindow)
	toks := core.TokenCounts{
		Input:        4976,
		Output:       2_621_000,
		CacheWrite5m: 6_244_000,
		CacheRead:    1_055_000_000,
	}
	week := core.PeriodUsage{
		Start:    wStart,
		End:      reset7d,
		DayIndex: 1,
		DayCount: 7,
		Cost:     308.82,
		Tokens:   toks,
		Requests: 2484,
		Models: []core.ModelUsage{
			{ModelID: "claude-opus-5-5", DisplayName: "Opus 5.5", Family: core.FamilyOpus, Cost: 308.82, Tokens: toks, Requests: 2484},
		},
		Categories: core.CategoryCosts{
			Input:      0.02,
			Output:     52.42,
			CacheWrite: 31.22,
			CacheRead:  211.00,
			Savings:    4010.0,
		},
	}
	dayStart, dayEnd := core.DayInterval(now)
	day := week
	day.Start, day.End, day.DayIndex, day.DayCount = dayStart, dayEnd, 1, 1
	day.Cost = 280.0

	bStart, bEnd := core.BillingInterval(now, 28, 22, 0, 0)
	billing := week
	billing.Start, billing.End, billing.DayIndex, billing.DayCount = bStart, bEnd, 22, 31
	billing.Cost = 10700.0

	s.snapshot = &core.UsageSnapshot{
		GeneratedAt: now,
		Billing:     billing,
		Day:         day,
		Week:        &week,
		WeeklyAllowance: &core.WeeklyAllowance{
			Start:       wStart,
			End:         reset7d,
			Utilization: 10.05,
			SyncedCost:  308.82,
			Spent:       308.82,
			SyncedAt:    &syncedAt,
		},
		Daily:        daily,
		TotalRecords: 2484,
		LifetimeCost: 10700.0,
	}

	// Build realistic speed history
	rates := []float64{48, 62, 54, 73, 72, 71, 70, 72, 71, 72, 71, 71}
	reqs := make([]core.TimedRequest, len(rates))
	for i, r := range rates {
		end := nowSec - float64((len(rates)-1-i)*160)
		dur := 12.0
		reqs[i] = core.TimedRequest{
			ID:     uint64(i + 1),
			Model:  "Opus 5.5",
			Start:  end - dur,
			End:    end,
			Output: int(r * dur),
		}
	}
	s.speedLog = core.SpeedLog{
		Requests: reqs,
		Pending: []core.PendingRequest{
			{ID: "p1", Model: "Opus 5.5", Start: nowSec - 13},
			{ID: "p2", Model: "Opus 5.5", Start: nowSec - 8},
			{ID: "p3", Model: "Opus 5.5", Start: nowSec - 3},
		},
	}

	s.setDemoAntigravityLocked(now)
}

func (s *Store) setDemoAntigravityLocked(now time.Time) {
	rem5h := 0.58
	remWk := 0.69
	remFull := 1.0
	agReset5h := now.Add(2*time.Hour + 18*time.Minute)
	agResetWk := now.Add(4 * 24 * time.Hour)
	ctxIn := int64(280_000)
	ctxOut := int64(62_000)
	ctxSize := int64(1_000_000)
	ctxPct := 34.2
	sessCost := 1.42
	captured := now.Add(-1 * time.Minute)

	snap := &core.AGStatusLineSnapshot{
		ConversationID:    "demo-ag-conv",
		ConversationTitle: "Antigravity Monitor Integration",
		Model:             &core.AGStatusLineModelInfo{ID: "gemini-4-neon-xhigh", DisplayName: "Gemini 4 Neon"},
		ContextWindow: &core.AGStatusLineContextWindow{
			TotalInputTokens:  &ctxIn,
			TotalOutputTokens: &ctxOut,
			ContextWindowSize: &ctxSize,
			UsedPercentage:    &ctxPct,
		},
		Quota: map[string]core.AGStatusLineQuotaBucket{
			"gemini-5h":     {RemainingFraction: &rem5h, ResetTime: agReset5h.UTC().Format(time.RFC3339)},
			"gemini-weekly": {RemainingFraction: &remWk, ResetTime: agResetWk.UTC().Format(time.RFC3339)},
			"3p-5h":         {RemainingFraction: &remFull, ResetTime: "1970-01-01T00:00:00Z"},
			"3p-weekly":     {RemainingFraction: &remFull, ResetTime: "1970-01-01T00:00:00Z"},
		},
		AgentState: "idle",
		PlanTier:   "Ultra",
		Email:      "demo@antigravity.dev",
		Cost:       &core.AGStatusLineCostInfo{TotalUSD: &sessCost},
		CapturedAt: &core.AGFlexibleTime{T: &captured},
	}
	s.agOfficial = core.EvaluateAGQuota(snap, now)
	s.agCLIPath = `C:\Users\demo\.local\bin\agy.exe`
	s.agBridge = core.AGBridgeStatus{Kind: core.AGBridgeInstalledDirect}
	s.agAuthRequired = false
	s.agFeedback = ""
	s.agProcs = []platform.ClaudeTask{
		{PID: 14472, Name: "Antigravity", Detail: "4 procs", Bytes: 642 * 1024 * 1024},
		{PID: 28128, Name: "Language Server", Detail: "PID 28128", Bytes: 198 * 1024 * 1024},
	}

	speedVals := []float64{52, 58, 61, 56, 65, 72, 68, 74, 70, 64, 67, 73, 69, 63, 71, 66, 70, 68}
	samples := make([]core.AGSpeedSample, len(speedVals))
	for i, sp := range speedVals {
		samples[i] = core.AGSpeedSample{
			ID:               i + 1,
			Timestamp:        now.Add(-time.Duration((len(speedVals)-1-i)*180) * time.Second),
			TokensPerSec:     sp,
			OutputTokens:     int64(sp * 14),
			ModelDisplayName: "Gemini 4 Neon",
		}
	}
	s5m := 68.0
	s1h := 65.4
	lastAge := 28.0

	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dailyToks := []int64{3_200_000, 7_400_000, 4_100_000, 10_200_000, 6_500_000, 9_800_000, 12_100_000, 6_900_000, 7_600_000, 11_400_000, 9_200_000, 12_500_000, 7_900_000, 11_800_000}
	daily := make([]core.AGDailyUsagePoint, len(dailyToks))
	for i, tot := range dailyToks {
		d := todayStart.AddDate(0, 0, -(len(dailyToks)-1)+i)
		daily[i] = core.AGDailyUsagePoint{
			DayKey:              d.Format("2006-01-02"),
			Date:                d,
			UncachedInputTokens: tot * 16 / 100,
			CacheReadTokens:     tot * 80 / 100,
			OutputTokens:        tot * 4 / 100,
			RequestCount:        int(tot / 145_000),
			EstimatedCostUSD:    float64(tot) / 1_000_000.0 * 2.1,
		}
	}

	cNeon := 125.68
	sNeon := 108.40
	cSonnet := 60.52
	sSonnet := 44.40
	weekModels := []core.AGModelUsageSummary{
		{
			Model: "gemini-4-neon-xhigh", DisplayName: "Gemini 4 Neon",
			UncachedInputTokens: 9_600_000, CacheReadTokens: 48_300_000, OutputTokens: 2_310_000,
			RequestCount: 412, EstimatedCostUSD: &cNeon, CacheSavingsUSD: &sNeon,
		},
		{
			Model: "claude-sonnet-4-5", DisplayName: "Claude Sonnet 4.5",
			UncachedInputTokens: 4_600_000, CacheReadTokens: 23_200_000, OutputTokens: 1_110_000,
			RequestCount: 200, EstimatedCostUSD: &cSonnet, CacheSavingsUSD: &sSonnet,
		},
	}

	cNeonToday := 22.50
	sNeonToday := 19.20
	cSonnetToday := 10.90
	sSonnetToday := 8.10
	todayModels := []core.AGModelUsageSummary{
		{
			Model: "gemini-4-neon-xhigh", DisplayName: "Gemini 4 Neon",
			UncachedInputTokens: 1_750_000, CacheReadTokens: 9_900_000, OutputTokens: 430_000,
			RequestCount: 74, EstimatedCostUSD: &cNeonToday, CacheSavingsUSD: &sNeonToday,
		},
		{
			Model: "claude-sonnet-4-5", DisplayName: "Claude Sonnet 4.5",
			UncachedInputTokens: 850_000, CacheReadTokens: 4_900_000, OutputTokens: 210_000,
			RequestCount: 38, EstimatedCostUSD: &cSonnetToday, CacheSavingsUSD: &sSonnetToday,
		},
	}

	cNeonMonth := 482.10
	sNeonMonth := 398.00
	cSonnetMonth := 230.40
	sSonnetMonth := 186.20
	monthModels := []core.AGModelUsageSummary{
		{
			Model: "gemini-4-neon-xhigh", DisplayName: "Gemini 4 Neon",
			UncachedInputTokens: 36_500_000, CacheReadTokens: 196_000_000, OutputTokens: 8_400_000,
			RequestCount: 1580, EstimatedCostUSD: &cNeonMonth, CacheSavingsUSD: &sNeonMonth,
		},
		{
			Model: "claude-sonnet-4-5", DisplayName: "Claude Sonnet 4.5",
			UncachedInputTokens: 17_500_000, CacheReadTokens: 94_000_000, OutputTokens: 4_100_000,
			RequestCount: 760, EstimatedCostUSD: &cSonnetMonth, CacheSavingsUSD: &sSonnetMonth,
		},
	}

	weekStart := agResetWk.Add(-7 * 24 * time.Hour)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	monthEnd := monthStart.AddDate(0, 1, 0).Add(-time.Second)
	earliest := now.AddDate(0, -1, -12)

	s.agLocal = core.AGLocalUsageReport{
		Today: core.AGUsageWindowSummary{
			UncachedInputTokens: 2_600_000, CacheReadTokens: 14_800_000, OutputTokens: 640_000,
			RequestCount: 112, EstimatedCostUSD: 33.40, CacheSavingsUSD: 27.30,
		},
		FiveHour: core.AGUsageWindowSummary{
			UncachedInputTokens: 1_200_000, CacheReadTokens: 7_400_000, OutputTokens: 310_000,
			RequestCount: 54, EstimatedCostUSD: 16.20, CacheSavingsUSD: 13.80,
		},
		Week: core.AGUsageWindowSummary{
			UncachedInputTokens: 14_200_000, CacheReadTokens: 71_500_000, OutputTokens: 3_420_000,
			RequestCount: 612, EstimatedCostUSD: 186.20, CacheSavingsUSD: 152.80,
		},
		Month: core.AGUsageWindowSummary{
			UncachedInputTokens: 54_000_000, CacheReadTokens: 290_000_000, OutputTokens: 12_500_000,
			RequestCount: 2340, EstimatedCostUSD: 712.50, CacheSavingsUSD: 584.20,
		},
		AllTime: core.AGUsageWindowSummary{
			UncachedInputTokens: 96_400_000, CacheReadTokens: 520_000_000, OutputTokens: 22_800_000,
			RequestCount: 4210, EstimatedCostUSD: 1284.60, CacheSavingsUSD: 1042.10,
		},
		EarliestRecordDate: &earliest,
		WeekPeriod: &core.AGBillingPeriodInfo{
			Start: weekStart, End: agResetWk.Add(-time.Second), CurrentDay: 4, TotalDays: 7, RemainingDays: 4,
		},
		MonthPeriod: &core.AGBillingPeriodInfo{
			Start: monthStart, End: monthEnd, CurrentDay: 14, TotalDays: 30, RemainingDays: 16,
		},
		OutputSpeed: core.AGSpeedSnapshot{
			Recent5mTokPerSec:  &s5m,
			Recent1hTokPerSec:  &s1h,
			Recent1hReplyCount: len(samples),
			LatestModelName:    "Gemini 4 Neon",
			Sparkline:          speedVals,
			Samples:            samples,
			IsRecentlyActive:   true,
			LastResponseAgeSec: &lastAge,
		},
		ByModelMonth:      monthModels,
		ByModelWeek:       weekModels,
		ByModelToday:      todayModels,
		DailyTrend:        daily,
		ConversationCount: 18,
		TotalRecords:      2340,
		UpdatedAt:         now,
	}
}
