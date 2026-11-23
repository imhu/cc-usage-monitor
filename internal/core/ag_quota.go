package core

import (
	"encoding/json"
	"math"
	"os"
	"sort"
	"strings"
	"time"
)

const AGQuotaStaleThreshold = 30 * time.Minute

func LoadAGSnapshot(path string) *AGStatusLineSnapshot {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil
	}
	var snap AGStatusLineSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil
	}
	if snap.CapturedTime() == nil {
		if info, statErr := os.Stat(path); statErr == nil {
			mt := info.ModTime().UTC()
			snap.CapturedAt = &AGFlexibleTime{T: &mt}
		}
	}
	return &snap
}

func EvaluateAGQuota(snap *AGStatusLineSnapshot, now time.Time) AGOfficialQuotaReport {
	if snap == nil {
		return AGOfficialQuotaReport{}
	}

	capturedAt := now
	if ct := snap.CapturedTime(); ct != nil {
		capturedAt = *ct
	}
	age := now.Sub(capturedAt)
	if age < 0 {
		age = 0
	}
	isStale := age > AGQuotaStaleThreshold

	buckets := make([]AGQuotaBucketEval, 0, len(snap.Quota))
	for bucketID, raw := range snap.Quota {
		kind := ClassifyAGWindow(bucketID, raw.ResetInSeconds)
		resetDate := resolveAGResetTime(raw, capturedAt)

		var remUntil *time.Duration
		isElapsed := false
		if resetDate != nil {
			delta := resetDate.Sub(now)
			if delta <= 0 {
				isElapsed = true
				zero := time.Duration(0)
				remUntil = &zero
			} else {
				remUntil = &delta
			}
		}

		rawRem := 1.0
		if raw.RemainingFraction != nil {
			rawRem = clamp01(*raw.RemainingFraction)
		}
		effRem := rawRem
		effUsed := clamp01(1.0 - rawRem)
		if isElapsed {
			effRem = 1.0
			effUsed = 0.0
		}

		safeLine := ComputeAGSafeLine(kind, resetDate, now, isElapsed)
		disabled := raw.Disabled != nil && *raw.Disabled

		buckets = append(buckets, AGQuotaBucketEval{
			BucketID:            bucketID,
			DisplayName:         AGBucketDisplayName(bucketID, kind, false),
			ShortName:           AGBucketShortName(bucketID, kind, false),
			WindowKind:          kind,
			UsedFraction:        effUsed,
			RemainingFraction:   effRem,
			SafeLineFraction:    safeLine,
			ResetTime:           resetDate,
			RemainingUntilReset: remUntil,
			IsResetElapsed:      isElapsed,
			IsDisabled:          disabled,
		})
	}

	sort.Slice(buckets, func(i, j int) bool {
		oi, oj := agWindowOrder(buckets[i].WindowKind), agWindowOrder(buckets[j].WindowKind)
		if oi != oj {
			return oi < oj
		}
		gi := strings.Contains(strings.ToLower(buckets[i].BucketID), "gemini")
		gj := strings.Contains(strings.ToLower(buckets[j].BucketID), "gemini")
		if gi != gj {
			return gi
		}
		return buckets[i].BucketID < buckets[j].BucketID
	})

	var primary5h, primaryWeekly *AGQuotaBucketEval
	for i := range buckets {
		b := &buckets[i]
		if b.WindowKind == AGWindowFiveHour {
			if primary5h == nil || (!b.IsDisabled && primary5h.IsDisabled) {
				primary5h = b
			}
		}
		if b.WindowKind == AGWindowWeekly {
			if primaryWeekly == nil || (!b.IsDisabled && primaryWeekly.IsDisabled) {
				primaryWeekly = b
			}
		}
	}

	activeModel := ""
	if snap.Model != nil {
		if snap.Model.DisplayName != "" {
			activeModel = snap.Model.DisplayName
		} else {
			activeModel = snap.Model.ID
		}
	}
	var sessionCost *float64
	if snap.Cost != nil && snap.Cost.TotalUSD != nil {
		v := *snap.Cost.TotalUSD
		sessionCost = &v
	}

	capCopy := capturedAt
	return AGOfficialQuotaReport{
		Buckets:            buckets,
		PrimaryFiveHour:    primary5h,
		PrimaryWeekly:      primaryWeekly,
		SnapshotCapturedAt: &capCopy,
		IsStale:            isStale,
		StaleAge:           age,
		ActiveModelName:    activeModel,
		ContextWindow:      snap.ContextWindow,
		ConversationTitle:  snap.ConversationTitle,
		AgentState:         snap.AgentState,
		PlanTier:           snap.PlanTier,
		Email:              snap.Email,
		SessionCostUSD:     sessionCost,
	}
}

func ClassifyAGWindow(bucketID string, resetInSec *int) AGQuotaWindowKind {
	lower := strings.ToLower(bucketID)
	if strings.Contains(lower, "5h") ||
		strings.Contains(lower, "5-hour") ||
		strings.Contains(lower, "five_hour") ||
		strings.Contains(lower, "five-hour") ||
		strings.Contains(lower, "session") ||
		strings.Contains(lower, "rolling") {
		return AGWindowFiveHour
	}
	if strings.Contains(lower, "week") || strings.Contains(lower, "7d") || strings.Contains(lower, "7-day") {
		return AGWindowWeekly
	}
	if resetInSec != nil {
		s := *resetInSec
		if s > 0 && s <= 6*3600 {
			return AGWindowFiveHour
		}
		if s > 24*3600 && s <= 8*24*3600 {
			return AGWindowWeekly
		}
	}
	return AGWindowOther
}

func ComputeAGSafeLine(kind AGQuotaWindowKind, resetDate *time.Time, now time.Time, isResetElapsed bool) *float64 {
	if isResetElapsed || resetDate == nil {
		return nil
	}
	dur := kind.Duration()
	if dur <= 0 {
		return nil
	}
	windowStart := resetDate.Add(-dur)
	elapsed := now.Sub(windowStart).Seconds()
	if elapsed < 0 {
		z := 0.0
		return &z
	}
	switch kind {
	case AGWindowFiveHour:
		v := clamp01(elapsed / dur.Seconds())
		return &v
	case AGWindowWeekly:
		dayIndex := math.Min(7.0, math.Floor(elapsed/86400.0)+1.0)
		v := clamp01(dayIndex / 7.0)
		return &v
	default:
		return nil
	}
}

func AGBucketDisplayName(bucketID string, kind AGQuotaWindowKind, zh bool) string {
	lower := strings.ToLower(bucketID)
	var group string
	switch {
	case strings.Contains(lower, "gemini"):
		group = "Gemini"
	case strings.Contains(lower, "3p") || strings.Contains(lower, "third"):
		if zh {
			group = "第三方模型 (3P)"
		} else {
			group = "Third-Party"
		}
	case strings.Contains(lower, "claude"):
		group = "Claude"
	case strings.Contains(lower, "gpt"):
		group = "GPT"
	default:
		cleaned := strings.ReplaceAll(lower, "-5h", "")
		cleaned = strings.ReplaceAll(cleaned, "-weekly", "")
		cleaned = strings.ReplaceAll(cleaned, "-", " ")
		cleaned = strings.ReplaceAll(cleaned, "_", " ")
		group = AGModelDisplayName(strings.ReplaceAll(strings.TrimSpace(cleaned), " ", "-"))
	}

	switch kind {
	case AGWindowFiveHour:
		if zh {
			return group + " 5小时"
		}
		return group + " 5-hour"
	case AGWindowWeekly:
		if zh {
			return group + " 每周"
		}
		return group + " Weekly"
	default:
		return group
	}
}

func AGBucketShortName(bucketID string, kind AGQuotaWindowKind, zh bool) string {
	lower := strings.ToLower(bucketID)
	prefix := ""
	switch {
	case strings.Contains(lower, "gemini"):
		prefix = "Gemini"
	case strings.Contains(lower, "3p") || strings.Contains(lower, "third"):
		prefix = "3P"
	case strings.Contains(lower, "claude"):
		prefix = "Claude"
	case strings.Contains(lower, "gpt"):
		prefix = "GPT"
	}
	var suffix string
	switch kind {
	case AGWindowFiveHour:
		suffix = "5h"
	case AGWindowWeekly:
		if zh {
			suffix = "周"
		} else {
			suffix = "Wk"
		}
	default:
		return prefix
	}
	if prefix == "" {
		return suffix
	}
	return prefix + " " + suffix
}

func resolveAGResetTime(raw AGStatusLineQuotaBucket, capturedAt time.Time) *time.Time {
	if raw.ResetTime != "" {
		if t, ok := ParseAGISO8601(raw.ResetTime); ok {
			return &t
		}
	}
	if raw.ResetInSeconds != nil {
		t := capturedAt.Add(time.Duration(*raw.ResetInSeconds) * time.Second)
		return &t
	}
	return nil
}

func agWindowOrder(k AGQuotaWindowKind) int {
	switch k {
	case AGWindowFiveHour:
		return 0
	case AGWindowWeekly:
		return 1
	default:
		return 2
	}
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
