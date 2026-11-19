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
}
