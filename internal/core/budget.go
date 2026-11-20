package core

import (
	"math"
	"time"
)

const (
	BudgetCapacity         = 5.0
	BudgetRefillInterval   = 20 * time.Minute
	BudgetManualReserve    = 1.0
	BudgetFirstCooldown    = 15 * time.Minute
	BudgetLongestCooldown  = 60 * time.Minute
	BudgetMaxSlowdown      = 3.0 // max 60m per token (never exceeds 60m wait)
	BudgetRecoveryHalfLife = 6 * time.Hour
)

// RequestBudget implements a persistent token bucket with 429 exponential backoff,
// matching kittors/claude-usage-monitor's RequestBudget.swift.
type RequestBudget struct {
	Tokens       float64   `json:"tokens"`
	UpdatedAt    time.Time `json:"updatedAt"`
	BlockedUntil time.Time `json:"blockedUntil"`
	Streak       int       `json:"streak"`
	Slowdown     float64   `json:"slowdown"`
	SlowedAt     time.Time `json:"slowedAt"`
}

func NewRequestBudget(now time.Time) RequestBudget {
	return RequestBudget{
		Tokens:    BudgetCapacity,
		UpdatedAt: now,
		Slowdown:  1,
	}
}

func (b RequestBudget) EffectiveSlowdown(now time.Time) float64 {
	if b.Slowdown <= 1 || b.SlowedAt.IsZero() {
		return 1
	}
	elapsed := now.Sub(b.SlowedAt)
	if elapsed <= 0 {
		return min(BudgetMaxSlowdown, b.Slowdown)
	}
	excess := b.Slowdown - 1
	halvings := elapsed.Seconds() / BudgetRecoveryHalfLife.Seconds()
	return min(BudgetMaxSlowdown, 1+excess*math.Pow(0.5, halvings))
}

func (b RequestBudget) RefillIntervalAt(at time.Time) time.Duration {
	return time.Duration(float64(BudgetRefillInterval) * b.EffectiveSlowdown(at))
}

func (b RequestBudget) Available(now time.Time) float64 {
	if b.UpdatedAt.IsZero() {
		return BudgetCapacity
	}
	elapsed := now.Sub(b.UpdatedAt)
	if elapsed <= 0 {
		return max(0, min(BudgetCapacity, b.Tokens))
	}
	interval := b.RefillIntervalAt(now).Seconds()
	if interval <= 0 {
		interval = BudgetRefillInterval.Seconds()
	}
	added := elapsed.Seconds() / interval
	return min(BudgetCapacity, max(0, b.Tokens)+added)
}

func (b RequestBudget) Cooldown(now time.Time) time.Duration {
	if b.BlockedUntil.IsZero() {
		return 0
	}
	rem := b.BlockedUntil.Sub(now)
	if rem <= 0 {
		return 0
	}
	return min(BudgetLongestCooldown, rem)
}

func (b RequestBudget) InCooldown(now time.Time) bool {
	return b.Cooldown(now) > 0
}

// Wait returns the longer of remaining 429 cooldown and token-refill wait.
func (b RequestBudget) Wait(manual bool, now time.Time) time.Duration {
	need := 1.0
	if !manual {
		need = 1.0 + BudgetManualReserve
	}
	missing := need - b.Available(now)
	var refill time.Duration
	if missing > 1e-9 {
		refill = time.Duration(missing * float64(b.RefillIntervalAt(now)))
	}
	return max(b.Cooldown(now), refill)
}

// RetryWait returns the remaining 429 cooldown wait until manual retry is allowed again.
func (b RequestBudget) RetryWait(now time.Time) time.Duration {
	if !b.InCooldown(now) {
		return 0
	}
	return b.Wait(true, now)
}

func (b RequestBudget) CanAuto(now time.Time) bool {
	return b.Wait(false, now) == 0
}

func (b RequestBudget) CanManual(now time.Time) bool {
	return b.Wait(true, now) == 0
}

func (b *RequestBudget) Consume(now time.Time) {
	avail := b.Available(now)
	b.Tokens = max(0, avail-1.0)
	b.UpdatedAt = now
}

func (b *RequestBudget) RecordSuccess(now time.Time) {
	b.Streak = 0
}

func (b *RequestBudget) RecordRateLimit(now time.Time, retryAfter time.Duration) {
	b.Streak++
	cooldown := BudgetFirstCooldown * time.Duration(1<<min(b.Streak-1, 4))
	if cooldown > BudgetLongestCooldown {
		cooldown = BudgetLongestCooldown
	}
	if retryAfter > BudgetLongestCooldown {
		retryAfter = BudgetLongestCooldown
	}
	wait := max(cooldown, retryAfter)
	b.BlockedUntil = now.Add(wait)
	if b.Streak == 1 {
		b.Slowdown = min(BudgetMaxSlowdown, b.EffectiveSlowdown(now)*2)
		b.SlowedAt = now
	}
	// Always keep 1 manual-refresh reserve token so manual refresh is ready as soon as cooldown ends,
	// while auto-sync still waits for 1 slowed refill interval (<= 60m).
	b.Tokens = BudgetManualReserve
	b.UpdatedAt = now
}
