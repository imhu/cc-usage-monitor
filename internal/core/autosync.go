package core

import "time"

const (
	AutoSyncMinSpacing   = 10 * time.Second
	AutoSyncDefaultInt   = 30 * time.Second
	AutoSyncMaxInt       = time.Hour
	AutoSyncActiveWindow = 5 * time.Minute
	AutoSyncMinStep      = 0.5
	AutoSyncLongestWait  = 20 * time.Minute
	AutoSyncSettleDelay  = 15 * time.Second
	AutoSyncPartialStep  = 0.3
)

type SyncOccasion int

const (
	OccasionTick SyncOccasion = iota
	OccasionResume
)

type SyncActivity struct {
	ClaudeCodeRunning bool
	LastConsumption   *time.Time
	HasNewUsage       bool
	// Progress is how many expected 1% steps have accumulated since last sync.
	Progress float64
}

func ClampSyncInterval(d time.Duration) time.Duration {
	if d < AutoSyncMinSpacing {
		return AutoSyncMinSpacing
	}
	if d > AutoSyncMaxInt {
		return AutoSyncMaxInt
	}
	return d
}

func HasRecentConsumption(a SyncActivity, now time.Time) bool {
	if a.LastConsumption == nil {
		return false
	}
	return now.Sub(*a.LastConsumption) <= AutoSyncActiveWindow
}

func IsInUse(a SyncActivity, now time.Time) bool {
	return a.ClaudeCodeRunning && HasRecentConsumption(a, now)
}

func ShouldAutoSync(interval time.Duration, occasion SyncOccasion, a SyncActivity, lastReq *time.Time, now time.Time) bool {
	if !IsInUse(a, now) || !a.HasNewUsage {
		return false
	}
	since := time.Duration(1<<62 - 1)
	if lastReq != nil {
		since = now.Sub(*lastReq)
	}
	if since < ClampSyncInterval(interval) {
		return false
	}
	if occasion == OccasionResume {
		return true
	}
	if a.Progress >= 1.0 || since >= AutoSyncLongestWait {
		return true
	}
	if a.LastConsumption != nil && now.Sub(*a.LastConsumption) >= AutoSyncSettleDelay {
		return a.Progress >= AutoSyncPartialStep
	}
	return false
}

func ShouldCheckOnOpen(a SyncActivity, lastReq *time.Time, now time.Time) bool {
	if !a.HasNewUsage {
		return false
	}
	since := time.Duration(1<<62 - 1)
	if lastReq != nil {
		since = now.Sub(*lastReq)
	}
	return a.Progress >= AutoSyncPartialStep || since >= AutoSyncLongestWait
}

// SyncWindow describes one rate-limit window (5-hour or 7-day) at the last sync.
type SyncWindow struct {
	Cost    float64
	Percent int
}

// ComputeStep matches AutoSyncPolicy.step in claude-usage-monitor:
// estimates USD needed for +1% using cost / (percent + 1), taking the minimum across windows
// and flooring at AutoSyncMinStep ($0.50).
func ComputeStep(windows []SyncWindow) float64 {
	if len(windows) == 0 {
		return AutoSyncMinStep
	}
	best := 0.0
	hasBest := false
	for _, w := range windows {
		p := w.Percent
		if p < 0 {
			p = 0
		}
		est := w.Cost / float64(p+1)
		if !hasBest || est < best {
			best = est
			hasBest = true
		}
	}
	if !hasBest || best < AutoSyncMinStep {
		return AutoSyncMinStep
	}
	return best
}

// ComputeStepProgress estimates how many 1% steps deltaCost represents across the tracked windows.
func ComputeStepProgress(deltaCost, windowSyncedCost float64, officialPct int) float64 {
	if deltaCost <= 0 {
		return 0
	}
	return deltaCost / ComputeStep([]SyncWindow{{Cost: windowSyncedCost, Percent: officialPct}})
}

// ComputeMultiWindowProgress computes accumulated 1%-step progress across multiple windows (5h + 7d).
func ComputeMultiWindowProgress(unsyncedCost float64, windows []SyncWindow) float64 {
	if unsyncedCost <= 0 {
		return 0
	}
	return unsyncedCost / ComputeStep(windows)
}
