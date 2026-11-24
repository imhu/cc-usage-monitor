package core

import (
	"math"
	"sort"
	"time"
)

func AggregateAGUsage(records []AGTranscriptRecord, conversationCount int, now time.Time, officialWeeklyReset *time.Time, billingDay int) AGLocalUsageReport {
	loc := now.Location()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	fiveHoursAgo := now.Add(-5 * time.Hour)

	var weekStart, weekEnd time.Time
	if officialWeeklyReset != nil && officialWeeklyReset.After(now) {
		weekStart = officialWeeklyReset.Add(-7 * 24 * time.Hour)
		weekEnd = officialWeeklyReset.Add(-time.Second)
	} else {
		weekStart = now.Add(-7 * 24 * time.Hour)
		weekEnd = now
	}
	weekPeriod := makeAGPeriodInfo(weekStart, weekEnd, now, 7)

	monthBoundsStart, monthBoundsEnd, hasMonthPeriod := resolveAGBillingMonthBounds(now, billingDay, officialWeeklyReset)
	var monthPeriod *AGBillingPeriodInfo
	monthStartForRecords := now.Add(-30 * 24 * time.Hour)
	if hasMonthPeriod {
		mp := makeAGPeriodInfo(monthBoundsStart, monthBoundsEnd, now, 30)
		monthPeriod = &mp
		monthStartForRecords = monthBoundsStart
	}

	var todayRecs, fiveHourRecs, weekRecs, monthRecs, allValidRecs []AGTranscriptRecord
	var earliest *time.Time
	ctxCutoff := now.Add(-6 * time.Hour)
	var latestCtxRec *AGTranscriptRecord
	for _, r := range records {
		if r.Timestamp.After(now) {
			continue
		}
		allValidRecs = append(allValidRecs, r)
		if r.TotalInputTokens() > 0 && !r.Timestamp.Before(ctxCutoff) {
			if latestCtxRec == nil || !r.Timestamp.Before(latestCtxRec.Timestamp) {
				recCopy := r
				latestCtxRec = &recCopy
			}
		}
		if earliest == nil || r.Timestamp.Before(*earliest) {
			t := r.Timestamp
			earliest = &t
		}
		if !r.Timestamp.Before(todayStart) {
			todayRecs = append(todayRecs, r)
		}
		if !r.Timestamp.Before(fiveHoursAgo) {
			fiveHourRecs = append(fiveHourRecs, r)
		}
		if !r.Timestamp.Before(weekStart) {
			weekRecs = append(weekRecs, r)
		}
		if !r.Timestamp.Before(monthStartForRecords) {
			monthRecs = append(monthRecs, r)
		}
	}

	var latestContext *AGContextEstimate
	if latestCtxRec != nil {
		usedTokens := latestCtxRec.TotalTokens()
		winSize := AGContextWindowSizeFor(latestCtxRec.Model)
		if usedTokens > 0 && winSize > 0 {
			pct := math.Min(100, math.Max(0, float64(usedTokens)/float64(winSize)*100.0))
			latestContext = &AGContextEstimate{
				UsedTokens:        usedTokens,
				ContextWindowSize: winSize,
				UsedPercentage:    pct,
				Model:             latestCtxRec.Model,
				ConversationID:    latestCtxRec.ConversationID,
				Timestamp:         latestCtxRec.Timestamp,
			}
		}
	}

	return AGLocalUsageReport{
		Today:              summarizeAGRecords(todayRecs),
		FiveHour:           summarizeAGRecords(fiveHourRecs),
		Week:               summarizeAGRecords(weekRecs),
		Month:              summarizeAGRecords(monthRecs),
		AllTime:            summarizeAGRecords(allValidRecs),
		EarliestRecordDate: earliest,
		WeekPeriod:         &weekPeriod,
		MonthPeriod:        monthPeriod,
		OutputSpeed:        EstimateAGOutputSpeed(records, now),
		LatestContext:      latestContext,
		ByModelMonth:       summarizeAGByModel(monthRecs),
		ByModelWeek:        summarizeAGByModel(weekRecs),
		ByModelToday:       summarizeAGByModel(todayRecs),
		DailyTrend:         buildAGDailyTrend(records, now, 14),
		ConversationCount:  conversationCount,
		TotalRecords:       len(records),
		UpdatedAt:          now,
	}
}

const (
	agMinDecodeMs    = int64(100)
	agMaxDecodeSpeed = 10000.0
	agHeldWindowMs   = int64(1000)
	agHeldShare      = int64(4)
	agMinRankTokens  = int64(50)
)

func EstimateAGOutputSpeed(records []AGTranscriptRecord, now time.Time) AGSpeedSnapshot {
	oneHourAgo := now.Add(-time.Hour)
	fiveMinAgo := now.Add(-5 * time.Minute)

	var recent []AGTranscriptRecord
	for _, r := range records {
		if !r.Timestamp.Before(oneHourAgo) && !r.Timestamp.After(now) && r.OutputTokens > 0 {
			recent = append(recent, r)
		}
	}
	if len(recent) == 0 {
		return AGSpeedSnapshot{}
	}
	sort.Slice(recent, func(i, j int) bool {
		return recent[i].Timestamp.Before(recent[j].Timestamp)
	})

	latest := recent[len(recent)-1]
	lastAge := math.Max(0, now.Sub(latest.Timestamp).Seconds())
	isActive := lastAge <= 60.0

	// Pass 1: Prefer exact DB decode timings (excluding TTFT) with burst filtering.
	var samples []AGSpeedSample
	for _, rec := range recent {
		switch {
		case rec.DecodeMs >= agMinDecodeMs:
			rate := float64(rec.OutputTokens) * 1000.0 / float64(rec.DecodeMs)
			if rate > agMaxDecodeSpeed || (rec.DecodeMs < agHeldWindowMs && rec.TTFTMs > agHeldShare*rec.DecodeMs) {
				continue
			}
			samples = append(samples, AGSpeedSample{
				Timestamp:        rec.Timestamp,
				TokensPerSec:     rate,
				OutputTokens:     rec.OutputTokens,
				ModelDisplayName: AGModelDisplayName(rec.Model),
				TTFTMs:           rec.TTFTMs,
				DurationMs:       rec.DurationMs,
				DecodeMs:         rec.DecodeMs,
				IsExact:          true,
			})
		case rec.OutputTokens >= 1000 && rec.DurationMs >= 1000:
			rate := float64(rec.OutputTokens) * 1000.0 / float64(rec.DurationMs)
			if rate > agMaxDecodeSpeed {
				continue
			}
			samples = append(samples, AGSpeedSample{
				Timestamp:        rec.Timestamp,
				TokensPerSec:     rate,
				OutputTokens:     rec.OutputTokens,
				ModelDisplayName: AGModelDisplayName(rec.Model),
				TTFTMs:           rec.TTFTMs,
				DurationMs:       rec.DurationMs,
				DecodeMs:         rec.DurationMs,
				IsExact:          true,
			})
		}
	}

	isExact := len(samples) > 0
	if !isExact {
		// Pass 2: Fallback to adjacent transcript turn gaps when DB step timings are unavailable.
		byConv := map[string][]AGTranscriptRecord{}
		for _, r := range recent {
			byConv[r.ConversationID] = append(byConv[r.ConversationID], r)
		}
		for _, convRecs := range byConv {
			sort.Slice(convRecs, func(i, j int) bool {
				return convRecs[i].Timestamp.Before(convRecs[j].Timestamp)
			})
			for i, rec := range convRecs {
				var dt float64
				if i > 0 {
					dt = rec.Timestamp.Sub(convRecs[i-1].Timestamp).Seconds()
				} else {
					dt = math.Max(3.0, float64(rec.OutputTokens)/45.0)
				}
				if dt < 2.0 || dt > 90.0 {
					continue
				}
				rawSpeed := float64(rec.OutputTokens) / dt
				clamped := math.Max(5.0, math.Min(220.0, rawSpeed))
				samples = append(samples, AGSpeedSample{
					Timestamp:        rec.Timestamp,
					TokensPerSec:     clamped,
					OutputTokens:     rec.OutputTokens,
					ModelDisplayName: AGModelDisplayName(rec.Model),
				})
			}
		}
	}

	sort.Slice(samples, func(i, j int) bool {
		return samples[i].Timestamp.Before(samples[j].Timestamp)
	})
	for i := range samples {
		samples[i].ID = i + 1
	}

	var samples5m []AGSpeedSample
	for _, s := range samples {
		if !s.Timestamp.Before(fiveMinAgo) {
			samples5m = append(samples5m, s)
		}
	}

	var avg5m, avg1h *float64
	if r5m := pickAGRankRates(samples5m); len(r5m) > 0 {
		v := medianFloat64(r5m)
		avg5m = &v
	}
	if r1h := pickAGRankRates(samples); len(r1h) > 0 {
		v := medianFloat64(r1h)
		avg1h = &v
	}

	var medianTTFT *int64
	if ttfts := collectAGPositiveTTFTs(samples5m); len(ttfts) > 0 {
		v := medianInt64(ttfts)
		medianTTFT = &v
	} else if ttfts := collectAGPositiveTTFTs(samples); len(ttfts) > 0 {
		v := medianInt64(ttfts)
		medianTTFT = &v
	}

	sparkStart := 0
	if len(samples) > 18 {
		sparkStart = len(samples) - 18
	}
	spark := make([]float64, 0, len(samples)-sparkStart)
	for i := sparkStart; i < len(samples); i++ {
		spark = append(spark, samples[i].TokensPerSec)
	}

	return AGSpeedSnapshot{
		Recent5mTokPerSec:  avg5m,
		Recent1hTokPerSec:  avg1h,
		Recent1hReplyCount: len(samples),
		LatestModelName:    AGModelDisplayName(latest.Model),
		Sparkline:          spark,
		Samples:            samples,
		IsRecentlyActive:   isActive,
		LastResponseAgeSec: &lastAge,
		MedianTTFTMs:       medianTTFT,
		IsExact:            isExact,
	}
}

func pickAGRankRates(samples []AGSpeedSample) []float64 {
	if len(samples) == 0 {
		return nil
	}
	ranked := make([]float64, 0, len(samples))
	all := make([]float64, 0, len(samples))
	for _, s := range samples {
		if s.TokensPerSec <= 0 {
			continue
		}
		all = append(all, s.TokensPerSec)
		if s.OutputTokens >= agMinRankTokens {
			ranked = append(ranked, s.TokensPerSec)
		}
	}
	if len(ranked) > 0 {
		return ranked
	}
	return all
}

func collectAGPositiveTTFTs(samples []AGSpeedSample) []int64 {
	var out []int64
	for _, s := range samples {
		if s.TTFTMs > 0 {
			out = append(out, s.TTFTMs)
		}
	}
	return out
}

func resolveAGBillingMonthBounds(now time.Time, billingDay int, clockRef *time.Time) (start, end time.Time, ok bool) {
	if billingDay < 1 || billingDay > 31 {
		return time.Time{}, time.Time{}, false
	}
	h, m, s := 0, 0, 0
	if clockRef != nil {
		locRef := clockRef.In(now.Location())
		h, m, s = locRef.Hour(), locRef.Minute(), locRef.Second()
	}
	rawStart, rawEnd := BillingInterval(now, billingDay, h, m, s)
	return rawStart, rawEnd.Add(-time.Second), true
}

func makeAGPeriodInfo(start, end, now time.Time, fallbackDays int) AGBillingPeriodInfo {
	totalSecs := math.Max(86400, end.Sub(start).Seconds()+1)
	totalDays := max(1, int(math.Round(totalSecs/86400.0)))
	if totalDays <= 0 {
		totalDays = fallbackDays
	}
	elapsedSecs := math.Max(0, now.Sub(start).Seconds())
	curDay := min(totalDays, max(1, int(math.Floor(elapsedSecs/86400.0))+1))
	remSecs := math.Max(0, end.Sub(now).Seconds())
	remDays := max(0, int(math.Ceil(remSecs/86400.0)))
	return AGBillingPeriodInfo{
		Start:         start,
		End:           end,
		CurrentDay:    curDay,
		TotalDays:     totalDays,
		RemainingDays: remDays,
	}
}

func summarizeAGRecords(records []AGTranscriptRecord) AGUsageWindowSummary {
	var uncached, cacheRead, output int64
	var costUSD, savingsUSD float64
	unpricedMap := map[string]bool{}

	for _, r := range records {
		uncached += r.InputTokens
		cacheRead += r.CacheReadTokens
		output += r.OutputTokens
		if p := AGPriceFor(r.Model); p != nil {
			costUSD += p.CostUSD(r.InputTokens, r.CacheReadTokens, r.OutputTokens)
			savingsUSD += p.CacheSavingsUSD(r.CacheReadTokens)
		} else {
			unpricedMap[r.Model] = true
		}
	}

	unpriced := make([]string, 0, len(unpricedMap))
	for m := range unpricedMap {
		unpriced = append(unpriced, m)
	}
	sort.Strings(unpriced)

	return AGUsageWindowSummary{
		UncachedInputTokens: uncached,
		CacheReadTokens:     cacheRead,
		OutputTokens:        output,
		RequestCount:        len(records),
		EstimatedCostUSD:    costUSD,
		CacheSavingsUSD:     savingsUSD,
		UnpricedModels:      unpriced,
	}
}

func summarizeAGByModel(records []AGTranscriptRecord) []AGModelUsageSummary {
	type acc struct {
		uncached  int64
		cacheRead int64
		output    int64
		count     int
	}
	grouped := map[string]*acc{}
	for _, r := range records {
		a := grouped[r.Model]
		if a == nil {
			a = &acc{}
			grouped[r.Model] = a
		}
		a.uncached += r.InputTokens
		a.cacheRead += r.CacheReadTokens
		a.output += r.OutputTokens
		a.count++
	}

	out := make([]AGModelUsageSummary, 0, len(grouped))
	for model, a := range grouped {
		var costPtr, savingsPtr *float64
		if p := AGPriceFor(model); p != nil {
			c := p.CostUSD(a.uncached, a.cacheRead, a.output)
			s := p.CacheSavingsUSD(a.cacheRead)
			costPtr = &c
			savingsPtr = &s
		}
		out = append(out, AGModelUsageSummary{
			Model:               model,
			DisplayName:         AGModelDisplayName(model),
			UncachedInputTokens: a.uncached,
			CacheReadTokens:     a.cacheRead,
			OutputTokens:        a.output,
			RequestCount:        a.count,
			EstimatedCostUSD:    costPtr,
			CacheSavingsUSD:     savingsPtr,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		ti, tj := out[i].TotalTokens(), out[j].TotalTokens()
		if ti != tj {
			return ti > tj
		}
		return out[i].Model < out[j].Model
	})
	return out
}

func buildAGDailyTrend(records []AGTranscriptRecord, now time.Time, days int) []AGDailyUsagePoint {
	loc := now.Location()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	dayStarts := make([]time.Time, days)
	for i := 0; i < days; i++ {
		dayStarts[i] = todayStart.AddDate(0, 0, -(days-1)+i)
	}
	earliest := dayStarts[0]

	type bucket struct {
		uncached  int64
		cacheRead int64
		output    int64
		requests  int
		cost      float64
	}
	byKey := map[string]*bucket{}
	for _, r := range records {
		if r.Timestamp.Before(earliest) || r.Timestamp.After(now) {
			continue
		}
		key := r.Timestamp.In(loc).Format("2006-01-02")
		b := byKey[key]
		if b == nil {
			b = &bucket{}
			byKey[key] = b
		}
		b.uncached += r.InputTokens
		b.cacheRead += r.CacheReadTokens
		b.output += r.OutputTokens
		b.requests++
		if p := AGPriceFor(r.Model); p != nil {
			b.cost += p.CostUSD(r.InputTokens, r.CacheReadTokens, r.OutputTokens)
		}
	}

	out := make([]AGDailyUsagePoint, len(dayStarts))
	for i, d := range dayStarts {
		key := d.Format("2006-01-02")
		b := byKey[key]
		if b == nil {
			b = &bucket{}
		}
		out[i] = AGDailyUsagePoint{
			DayKey:              key,
			Date:                d,
			UncachedInputTokens: b.uncached,
			CacheReadTokens:     b.cacheRead,
			OutputTokens:        b.output,
			RequestCount:        b.requests,
			EstimatedCostUSD:    b.cost,
		}
	}
	return out
}
