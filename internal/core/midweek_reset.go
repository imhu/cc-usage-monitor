package core

import (
	"math"
	"time"
)

// MidweekResetMinDrop is the minimum percentage-point drop within the same
// weekly window required to treat an official utilization drop as a mid-week reset.
const MidweekResetMinDrop = 5.0

// MidweekReset records an official mid-week reset of the weekly quota where the
// weekly reset timestamp stayed the same (within 60s) while utilization dropped
// by at least MidweekResetMinDrop percentage points.
type MidweekReset struct {
	Before  WeeklyReading `json:"before"`
	After   WeeklyReading `json:"after"`
	WeekEnd time.Time     `json:"weekEnd"`
}

// MidweekResetBetween reports a MidweekReset if before and after belong to the
// same weekly window (within 60s), are chronologically ordered, and utilization
// dropped by at least MidweekResetMinDrop points.
func MidweekResetBetween(before WeeklyReading, beforeEnd time.Time, after WeeklyReading, afterEnd time.Time) *MidweekReset {
	if math.Abs(beforeEnd.Sub(afterEnd).Seconds()) >= 60 {
		return nil
	}
	if !after.At.After(before.At) {
		return nil
	}
	if !(before.Utilization-after.Utilization >= MidweekResetMinDrop) {
		return nil
	}
	return &MidweekReset{
		Before:  before,
		After:   after,
		WeekEnd: afterEnd,
	}
}

// BelongsToWeekEnding reports whether this mid-week reset belongs to the week ending at end.
func (m MidweekReset) BelongsToWeekEnding(end time.Time) bool {
	return math.Abs(m.WeekEnd.Sub(end).Seconds()) < 60
}

// Equal reports whether m and other represent the same mid-week reset.
func (m *MidweekReset) Equal(other *MidweekReset) bool {
	if m == nil || other == nil {
		return m == other
	}
	return m.Before.Utilization == other.Before.Utilization &&
		m.Before.At.Equal(other.Before.At) &&
		m.After.Utilization == other.After.Utilization &&
		m.After.At.Equal(other.After.At) &&
		m.WeekEnd.Equal(other.WeekEnd)
}
