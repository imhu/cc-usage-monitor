package core

import (
	"math"
	"sort"
	"time"
)

type WeeklyReading struct {
	Utilization float64   `json:"utilization"`
	At          time.Time `json:"at"`
}

type AllowanceBasis struct {
	Utilization float64   `json:"utilization"`
	SyncedCost  float64   `json:"syncedCost"`
	At          time.Time `json:"at"`
}

func (a AllowanceBasis) Full() float64 {
	if a.Utilization <= 0 {
		return 0
	}
	return a.SyncedCost * 100.0 / a.Utilization
}

type UsageSettings struct {
	BillingAnchorDay    int
	BillingAnchorHour   int
	BillingAnchorMinute int
	BillingAnchorSecond int
	WeeklyReset         *time.Time
	FiveHourReset       *time.Time
	WeeklyReading       *WeeklyReading
	EarlierAllowance    *AllowanceBasis
	MidweekReset        *MidweekReset
}

type WeeklyAllowance struct {
	Start       time.Time
	End         time.Time
	Utilization float64
	SyncedCost  float64
	Spent       float64
	SyncedAt    *time.Time
	Borrowed    bool
	RestartedAt *time.Time
	Forgiven    float64
	Reset       *MidweekReset
}

const MinWeeklyUtilizationForBasis = 10.0

func (a WeeklyAllowance) Full() float64 {
	if a.Utilization <= 0 {
		return 0
	}
	return a.SyncedCost * 100.0 / a.Utilization
}

func (a WeeklyAllowance) Remaining() float64 {
	return max(0, a.Full()-a.Spent)
}

// FullWeek returns the total local spend for the week when the active weekly
// allowance reaches 100%, including spend forgiven by a mid-week reset.
func (a WeeklyAllowance) FullWeek() float64 {
	return a.Forgiven + a.Full()
}

// Admits reports whether another full-week allowance estimate is consistent
// with this reading within a 2x sanity band. Because official utilization is
// floored to an integer, reading u% implies true utilization in [u, u+1), so
// the implied full allowance lies in [SyncedCost*100/(u+1), SyncedCost*100/u].
func (a WeeklyAllowance) Admits(otherFull float64) bool {
	if !(a.Utilization > 0 && a.SyncedCost > 0 && !math.IsNaN(otherFull) && !math.IsInf(otherFull, 0) && otherFull > 0) {
		return true
	}
	low := a.SyncedCost * 100.0 / (a.Utilization + 1.0)
	high := a.SyncedCost * 100.0 / a.Utilization
	return otherFull >= low/2.0 && otherFull <= high*2.0
}

func (a WeeklyAllowance) Ahead(from, end time.Time) float64 {
	seg := from
	if a.Start.After(seg) {
		seg = a.Start
	}
	weekEnd := a.End
	left := a.Remaining()
	for !weekEnd.After(seg) {
		weekEnd = weekEnd.Add(WeekWindow)
		left = a.Full()
	}
	total := 0.0
	for seg.Before(end) {
		span := weekEnd.Sub(seg).Seconds()
		segEnd := weekEnd
		if end.Before(segEnd) {
			segEnd = end
		}
		if span > 0 {
			total += left * segEnd.Sub(seg).Seconds() / span
		}
		seg = weekEnd
		weekEnd = weekEnd.Add(WeekWindow)
		left = a.Full()
	}
	return total
}

type Projection struct {
	Cost    float64
	RunsOut *time.Time
}

type ModelUsage struct {
	ModelID     string
	DisplayName string
	Family      ModelFamily
	Cost        float64
	Tokens      TokenCounts
	Requests    int
	IsEstimated bool
}

type DayUsage struct {
	Date     time.Time
	Cost     float64
	Tokens   TokenCounts
	Requests int
}

type CategoryCosts struct {
	Input      float64
	Output     float64
	CacheWrite float64
	CacheRead  float64
	Savings    float64
}

func (c *CategoryCosts) add(t TokenCounts, price ModelPrice) {
	p := price.Tier(t)
	c.Input += float64(t.Input) * p.Input / 1e6
	c.Output += float64(t.Output) * p.Output / 1e6
	c.CacheWrite += (float64(t.CacheWrite5m)*p.CacheWrite5m + float64(t.CacheWrite1h)*p.CacheWrite1h) / 1e6
	c.CacheRead += float64(t.CacheRead) * p.CacheRead / 1e6
	c.Savings += float64(t.CacheRead) * (p.Input - p.CacheRead) / 1e6
}

type PeriodUsage struct {
	Start      time.Time
	End        time.Time
	DayIndex   int
	DayCount   int
	Cost       float64
	Tokens     TokenCounts
	Requests   int
	Models     []ModelUsage
	Categories CategoryCosts
}

func (p PeriodUsage) elapsed(now time.Time, minDur time.Duration) float64 {
	length := max(1.0, p.End.Sub(p.Start).Seconds())
	minSec := min(minDur.Seconds(), length)
	el := now.Sub(p.Start).Seconds()
	if el < minSec {
		el = minSec
	}
	if el > length {
		el = length
	}
	return el
}

func (p PeriodUsage) DailyAverage(now time.Time) float64 {
	return p.Cost / (p.elapsed(now, 24*time.Hour) / 86400.0)
}

func (p PeriodUsage) ProjectedCost(now time.Time) float64 {
	if p.DayCount == 1 {
		length := p.End.Sub(p.Start).Seconds()
		return p.Cost * length / p.elapsed(now, 6*time.Hour)
	}
	return p.DailyAverage(now) * float64(p.DayCount)
}

func (p PeriodUsage) Projection(now time.Time, allowance *WeeklyAllowance) *Projection {
	if p.DayCount == 1 {
		return p.pacedDay(now, allowance)
	}
	if allowance == nil {
		return nil
	}
	if p.Start.Equal(allowance.Start) && p.End.Equal(allowance.End) {
		return &Projection{Cost: max(p.Cost, allowance.FullWeek())}
	}
	return &Projection{Cost: p.Cost + allowance.Ahead(now, p.End)}
}

func (p PeriodUsage) pacedDay(now time.Time, allowance *WeeklyAllowance) *Projection {
	linear := p.ProjectedCost(now)
	el := p.elapsed(now, 6*time.Hour)
	if allowance == nil || p.Cost <= 0 || el >= p.End.Sub(p.Start).Seconds() {
		return &Projection{Cost: linear}
	}
	rate := p.Cost / el
	t := p.Start.Add(time.Duration(el * float64(time.Second)))
	weekEnd := allowance.End
	left := allowance.Remaining()
	for !weekEnd.After(t) {
		weekEnd = weekEnd.Add(WeekWindow)
		left = allowance.Full()
	}
	total := p.Cost
	var runsOut *time.Time
	for t.Before(p.End) {
		segEnd := weekEnd
		if p.End.Before(segEnd) {
			segEnd = p.End
		}
		wanted := rate * segEnd.Sub(t).Seconds()
		if wanted > left {
			if runsOut == nil && rate > 0 {
				ro := t.Add(time.Duration((left / rate) * float64(time.Second)))
				runsOut = &ro
			}
			total += left
		} else {
			total += wanted
		}
		t = segEnd
		weekEnd = weekEnd.Add(WeekWindow)
		left = allowance.Full()
	}
	if runsOut != nil && total < linear {
		return &Projection{Cost: total, RunsOut: runsOut}
	}
	return &Projection{Cost: linear}
}

type UsageSnapshot struct {
	GeneratedAt     time.Time
	Billing         PeriodUsage
	Day             PeriodUsage
	Week            *PeriodUsage
	WeeklyAllowance *WeeklyAllowance
	FiveHourCost    *float64
	Daily           []DayUsage // last 30 days ascending, ending today
	TotalRecords    int
	LifetimeCost    float64
	FirstRecord     *time.Time
	LastRecord      *time.Time
}

func (s UsageSnapshot) Today() *DayUsage {
	if len(s.Daily) == 0 {
		return nil
	}
	return &s.Daily[len(s.Daily)-1]
}

func (s UsageSnapshot) HasData() bool {
	return s.TotalRecords > 0
}

func BillingInterval(now time.Time, anchorDay, hour, minute, second int) (time.Time, time.Time) {
	loc := now.Location()
	if anchorDay < 1 {
		anchorDay = 1
	}
	if anchorDay > 31 {
		anchorDay = 31
	}
	anchorInMonth := func(y int, m time.Month) time.Time {
		lastDay := time.Date(y, m+1, 0, 0, 0, 0, 0, loc).Day()
		d := min(anchorDay, lastDay)
		return time.Date(y, m, d, hour, minute, second, 0, loc)
	}
	cur := anchorInMonth(now.Year(), now.Month())
	if !now.Before(cur) {
		next := cur.AddDate(0, 1, 0)
		return cur, anchorInMonth(next.Year(), next.Month())
	}
	prev := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, loc)
	return anchorInMonth(prev.Year(), prev.Month()), cur
}

func DayInterval(now time.Time) (time.Time, time.Time) {
	loc := now.Location()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	return start, start.AddDate(0, 0, 1)
}

func WeeklyInterval(now, reset time.Time) (time.Time, time.Time) {
	end := reset
	for !end.After(now) {
		end = end.Add(WeekWindow)
	}
	for end.Add(-WeekWindow).After(now) {
		end = end.Add(-WeekWindow)
	}
	return end.Add(-WeekWindow), end
}

func ComputeSnapshot(idx IndexSnapshot, settings UsageSettings, now time.Time) UsageSnapshot {
	records := idx.Records
	infos := make([]ModelInfo, len(idx.Models))
	for i, m := range idx.Models {
		infos[i] = ModelInfoFor(m)
	}
	costs := make([]float64, len(records))
	lifetime := 0.0
	for i, r := range records {
		if int(r.ModelIdx) < len(infos) && infos[r.ModelIdx].Price != nil {
			c := infos[r.ModelIdx].Price.Cost(r.Tokens, r.IsFast) + float64(r.WebSearches)*WebSearchCost
			costs[i] = c
			lifetime += c
		}
	}

	mStart, mEnd := BillingInterval(now, settings.BillingAnchorDay, settings.BillingAnchorHour, settings.BillingAnchorMinute, settings.BillingAnchorSecond)
	dStart, dEnd := DayInterval(now)

	billing := summarizePeriod(records, costs, infos, mStart, mEnd, now)
	day := summarizePeriod(records, costs, infos, dStart, dEnd, now)

	var week *PeriodUsage
	var allowance *WeeklyAllowance
	if settings.WeeklyReset != nil {
		wStart, wEnd := WeeklyInterval(now, *settings.WeeklyReset)
		w := summarizePeriod(records, costs, infos, wStart, wEnd, now)
		week = &w
		allowance = computeWeeklyAllowance(records, costs, wStart, wEnd, w.Cost, settings.WeeklyReading, settings.EarlierAllowance, settings.MidweekReset)
	}

	var fiveHourCost *float64
	if settings.FiveHourReset != nil && settings.FiveHourReset.After(now) {
		fhStart := settings.FiveHourReset.Add(-FiveHourWindow)
		c := sumCostInRange(records, costs, fhStart, *settings.FiveHourReset)
		fiveHourCost = &c
	}

	daily := computeDaily(records, costs, now, 30)

	var firstRec, lastRec *time.Time
	if len(records) > 0 {
		ft := time.Unix(0, int64(records[0].Time*1e9))
		lt := time.Unix(0, int64(records[len(records)-1].Time*1e9))
		firstRec = &ft
		lastRec = &lt
	}

	return UsageSnapshot{
		GeneratedAt:     now,
		Billing:         billing,
		Day:             day,
		Week:            week,
		WeeklyAllowance: allowance,
		FiveHourCost:    fiveHourCost,
		Daily:           daily,
		TotalRecords:    len(records),
		LifetimeCost:    lifetime,
		FirstRecord:     firstRec,
		LastRecord:      lastRec,
	}
}

func summarizePeriod(records []UsageRecord, costs []float64, infos []ModelInfo, start, end, now time.Time) PeriodUsage {
	sSec := float64(start.UnixNano()) / 1e9
	eSec := float64(end.UnixNano()) / 1e9

	lo := sort.Search(len(records), func(i int) bool { return records[i].Time >= sSec })

	var totalCost float64
	var totalTokens TokenCounts
	var reqs int
	var cats CategoryCosts
	byModel := map[string]*ModelUsage{}

	for i := lo; i < len(records) && records[i].Time < eSec; i++ {
		r := records[i]
		c := costs[i]
		totalCost += c
		totalTokens.Add(r.Tokens)
		reqs++
		info := ModelInfo{ID: "unknown", DisplayName: "Unknown", Family: FamilyOther}
		if int(r.ModelIdx) < len(infos) {
			info = infos[r.ModelIdx]
		}
		if info.Price != nil {
			cats.add(r.Tokens, *info.Price)
		}
		mu := byModel[info.DisplayName]
		if mu == nil {
			mu = &ModelUsage{
				ModelID:     info.ID,
				DisplayName: info.DisplayName,
				Family:      info.Family,
				IsEstimated: info.IsEstimated,
			}
			byModel[info.DisplayName] = mu
		}
		mu.Cost += c
		mu.Tokens.Add(r.Tokens)
		mu.Requests++
	}

	models := make([]ModelUsage, 0, len(byModel))
	for _, m := range byModel {
		models = append(models, *m)
	}
	sort.Slice(models, func(i, j int) bool {
		if models[i].Cost != models[j].Cost {
			return models[i].Cost > models[j].Cost
		}
		return models[i].DisplayName < models[j].DisplayName
	})

	dayCount := max(1, int(math.Round(end.Sub(start).Hours()/24.0)))
	dayIndex := int(math.Floor(now.Sub(start).Hours()/24.0)) + 1
	if dayIndex < 1 {
		dayIndex = 1
	}
	if dayIndex > dayCount {
		dayIndex = dayCount
	}

	return PeriodUsage{
		Start:      start,
		End:        end,
		DayIndex:   dayIndex,
		DayCount:   dayCount,
		Cost:       totalCost,
		Tokens:     totalTokens,
		Requests:   reqs,
		Models:     models,
		Categories: cats,
	}
}

func unixSec(t time.Time) float64 {
	return float64(t.Unix()) + float64(t.Nanosecond())/1e9
}

func timeFromUnixSec(sec float64) time.Time {
	s, frac := math.Modf(sec)
	ns := int64(math.Round(frac * 1e9))
	if ns >= 1e9 {
		s += 1
		ns -= 1e9
	} else if ns < 0 {
		s -= 1
		ns += 1e9
	}
	return time.Unix(int64(s), ns)
}

func lowerBound(records []UsageRecord, t time.Time) int {
	sec := unixSec(t)
	return sort.Search(len(records), func(i int) bool { return records[i].Time >= sec })
}

func sumCosts(costs []float64, from, to int) float64 {
	if to <= from {
		return 0
	}
	sum := 0.0
	for i := from; i < to; i++ {
		sum += costs[i]
	}
	return sum
}

func sumCostInRange(records []UsageRecord, costs []float64, start, end time.Time) float64 {
	sSec := unixSec(start)
	eSec := unixSec(end)
	lo := sort.Search(len(records), func(i int) bool { return records[i].Time >= sSec })
	sum := 0.0
	for i := lo; i < len(records) && records[i].Time < eSec; i++ {
		sum += costs[i]
	}
	return sum
}

func computeWeeklyAllowance(records []UsageRecord, costs []float64, wStart, wEnd time.Time, weekCost float64, reading *WeeklyReading, earlier *AllowanceBasis, knownReset *MidweekReset) *WeeklyAllowance {
	var reset *MidweekReset
	if knownReset != nil && knownReset.BelongsToWeekEnding(wEnd) {
		c := *knownReset
		reset = &c
	}
	if reset == nil && earlier != nil && reading != nil && !earlier.At.Before(wStart) && earlier.At.Before(reading.At) {
		reset = MidweekResetBetween(
			WeeklyReading{Utilization: earlier.Utilization, At: earlier.At}, wEnd,
			*reading, wEnd,
		)
	}

	weekFrom := lowerBound(records, wStart)
	from := weekFrom
	var restartedAt *time.Time
	forgiven := 0.0
	if reset != nil {
		at := restartTime(*reset, records, costs, wStart)
		from = lowerBound(records, at)
		forgiven = sumCosts(costs, weekFrom, max(weekFrom, from))
		restartedAt = &at
	}

	spent := max(0.0, weekCost-forgiven)
	made := func(util, synced float64, at time.Time, borrowed bool) *WeeklyAllowance {
		t := at
		return &WeeklyAllowance{
			Start:       wStart,
			End:         wEnd,
			Utilization: util,
			SyncedCost:  synced,
			Spent:       spent,
			SyncedAt:    &t,
			Borrowed:    borrowed,
			RestartedAt: restartedAt,
			Forgiven:    forgiven,
			Reset:       reset,
		}
	}

	floorStart := wStart
	if restartedAt != nil {
		floorStart = *restartedAt
	}
	var current *WeeklyAllowance
	if reading != nil && !math.IsNaN(reading.Utilization) && !math.IsInf(reading.Utilization, 0) &&
		reading.Utilization > 0 && !reading.At.Before(floorStart) && reading.At.Before(wEnd) {
		to := lowerBound(records, reading.At)
		synced := sumCosts(costs, from, max(from, to))
		if synced > 0 {
			current = made(reading.Utilization, synced, reading.At, false)
		}
	}

	if current != nil && current.Utilization >= MinWeeklyUtilizationForBasis {
		return current
	}
	if earlier != nil && !math.IsNaN(earlier.Utilization) && !math.IsInf(earlier.Utilization, 0) &&
		earlier.Utilization >= MinWeeklyUtilizationForBasis &&
		!math.IsNaN(earlier.SyncedCost) && !math.IsInf(earlier.SyncedCost, 0) &&
		earlier.SyncedCost > 0 {
		if current != nil && !current.Admits(earlier.Full()) {
			return current
		}
		return made(earlier.Utilization, earlier.SyncedCost, earlier.At, true)
	}
	return current
}

// restartTime estimates when Anthropic reset the weekly quota mid-week.
// Using the pre-reset cost/utilization ratio to translate post-reset utilization
// back into local cost, it walks backward from reset.After.At toward reset.Before.At
// until reaching that target cost.
func restartTime(reset MidweekReset, records []UsageRecord, costs []float64, wStart time.Time) time.Time {
	earliest := reset.Before.At
	if wStart.After(earliest) {
		earliest = wStart
	}
	latest := reset.After.At
	if earliest.After(latest) {
		latest = earliest
	}
	weekFrom := lowerBound(records, wStart)
	lower := lowerBound(records, earliest)
	upper := lowerBound(records, latest)
	beforeCost := sumCosts(costs, weekFrom, max(weekFrom, lower))
	if !(reset.Before.Utilization > 0 && beforeCost > 0) {
		return latest
	}
	target := beforeCost / reset.Before.Utilization * max(0.0, reset.After.Utilization)
	if !(target > 0) {
		return latest
	}
	sum := 0.0
	for i := upper - 1; i >= lower; i-- {
		sum += costs[i]
		if sum >= target {
			return timeFromUnixSec(records[i].Time)
		}
	}
	return earliest
}

func computeDaily(records []UsageRecord, costs []float64, now time.Time, days int) []DayUsage {
	loc := now.Location()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	firstDay := todayStart.AddDate(0, 0, -(days - 1))
	out := make([]DayUsage, days)
	for i := 0; i < days; i++ {
		out[i].Date = firstDay.AddDate(0, 0, i)
	}
	startSec := float64(firstDay.UnixNano()) / 1e9
	endSec := float64(todayStart.AddDate(0, 0, 1).UnixNano()) / 1e9
	lo := sort.Search(len(records), func(i int) bool { return records[i].Time >= startSec })
	for i := lo; i < len(records) && records[i].Time < endSec; i++ {
		r := records[i]
		t := time.Unix(0, int64(r.Time*1e9)).In(loc)
		d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
		idx := int(math.Round(d.Sub(firstDay).Hours() / 24.0))
		if idx >= 0 && idx < days {
			out[idx].Cost += costs[i]
			out[idx].Tokens.Add(r.Tokens)
			out[idx].Requests++
		}
	}
	return out
}
