package core

import (
	"math"
	"sort"
	"time"
)

const (
	FiveHourWindow = 5 * time.Hour
	WeekWindow     = 7 * 24 * time.Hour
	SettlingShare  = 0.1
)

// PaceLine represents the safe usage threshold at a point in time.
type PaceLine struct {
	Fraction float64
	Elapsed  time.Duration
	Day      int // 1..7 for weekly, 0 for 5-hour
	Settling bool
}

func (l PaceLine) IsExceeded(usage float64) bool {
	return !l.Settling && usage > l.Fraction+1e-9
}

func FiveHourPace(resetsAt *time.Time, now time.Time) *PaceLine {
	elapsed, ok := windowElapsed(resetsAt, FiveHourWindow, now)
	if !ok {
		return nil
	}
	return &PaceLine{
		Fraction: elapsed.Seconds() / FiveHourWindow.Seconds(),
		Elapsed:  elapsed,
		Day:      0,
		Settling: elapsed.Seconds() < FiveHourWindow.Seconds()*SettlingShare,
	}
}

func WeeklyPace(resetsAt *time.Time, now time.Time) *PaceLine {
	elapsed, ok := windowElapsed(resetsAt, WeekWindow, now)
	if !ok {
		return nil
	}
	day := min(7, int(elapsed.Seconds()/86400)+1)
	return &PaceLine{
		Fraction: float64(day) / 7.0,
		Elapsed:  elapsed,
		Day:      day,
		Settling: false,
	}
}

func windowElapsed(resetsAt *time.Time, window time.Duration, now time.Time) (time.Duration, bool) {
	if resetsAt == nil || !resetsAt.After(now) {
		return 0, false
	}
	elapsed := window - resetsAt.Sub(now)
	if elapsed < 0 {
		return 0, false
	}
	return elapsed, true
}

// LargestRemainder apportions an integer total across weights so parts sum exactly to total.
func LargestRemainder(weights []float64, total int) []int {
	clean := make([]float64, len(weights))
	sum := 0.0
	for i, w := range weights {
		if !math.IsNaN(w) && !math.IsInf(w, 0) && w > 0 {
			clean[i] = w
			sum += w
		}
	}
	if total <= 0 || sum <= 0 {
		return make([]int, len(weights))
	}
	quotas := make([]float64, len(weights))
	for i, w := range clean {
		quotas[i] = w / sum * float64(total)
	}
	return RoundQuotas(quotas, total)
}

func RoundQuotas(quotas []float64, total int) []int {
	parts := make([]int, len(quotas))
	if total <= 0 {
		return parts
	}
	clean := make([]float64, len(quotas))
	sumParts := 0
	for i, q := range quotas {
		if !math.IsNaN(q) && !math.IsInf(q, 0) && q > 0 {
			clean[i] = q
			p := int(math.Floor(q))
			parts[i] = p
			sumParts += p
		}
	}
	left := total - sumParts
	if left < 0 || left > len(clean) {
		return LargestRemainder(clean, total)
	}
	order := make([]int, len(clean))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		ri := clean[order[i]] - float64(parts[order[i]])
		rj := clean[order[j]] - float64(parts[order[j]])
		return ri > rj
	})
	for _, idx := range order {
		if left <= 0 {
			break
		}
		parts[idx]++
		left--
	}
	return parts
}
