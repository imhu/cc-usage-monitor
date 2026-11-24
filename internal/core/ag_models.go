package core

import (
	"encoding/json"
	"math"
	"strings"
	"time"
)

// AGTranscriptRecord represents a parsed model-response record from
// ~/.gemini/antigravity/brain/<id>/.system_generated/logs/transcript.jsonl.
type AGTranscriptRecord struct {
	Timestamp       time.Time
	Model           string
	InputTokens     int64
	CacheReadTokens int64
	OutputTokens    int64
	ConversationID  string
	StepIndex       int
	HasStepIndex    bool
	TTFTMs          int64
	DurationMs      int64
	DecodeMs        int64
}

func (r AGTranscriptRecord) TotalInputTokens() int64 {
	return r.InputTokens + r.CacheReadTokens
}

func (r AGTranscriptRecord) TotalTokens() int64 {
	return r.InputTokens + r.CacheReadTokens + r.OutputTokens
}

type AGUsageWindowSummary struct {
	UncachedInputTokens int64
	CacheReadTokens     int64
	OutputTokens        int64
	RequestCount        int
	EstimatedCostUSD    float64
	CacheSavingsUSD     float64
	UnpricedModels      []string
}

func (s AGUsageWindowSummary) TotalInputTokens() int64 {
	return s.UncachedInputTokens + s.CacheReadTokens
}

func (s AGUsageWindowSummary) TotalTokens() int64 {
	return s.UncachedInputTokens + s.CacheReadTokens + s.OutputTokens
}

// CacheHitRate returns the fraction of input tokens served from cache in [0, 1].
func (s AGUsageWindowSummary) CacheHitRate() float64 {
	denom := s.TotalInputTokens()
	if denom <= 0 {
		return 0
	}
	return float64(s.CacheReadTokens) / float64(denom)
}

func (s AGUsageWindowSummary) HasUnpricedModels() bool {
	return len(s.UnpricedModels) > 0
}

type AGModelUsageSummary struct {
	Model               string
	DisplayName         string
	UncachedInputTokens int64
	CacheReadTokens     int64
	OutputTokens        int64
	RequestCount        int
	EstimatedCostUSD    *float64
	CacheSavingsUSD     *float64
}

func (m AGModelUsageSummary) TotalInputTokens() int64 {
	return m.UncachedInputTokens + m.CacheReadTokens
}

func (m AGModelUsageSummary) TotalTokens() int64 {
	return m.UncachedInputTokens + m.CacheReadTokens + m.OutputTokens
}

func (m AGModelUsageSummary) CacheHitRate() float64 {
	denom := m.TotalInputTokens()
	if denom <= 0 {
		return 0
	}
	return float64(m.CacheReadTokens) / float64(denom)
}

func (m AGModelUsageSummary) IsPriced() bool {
	return m.EstimatedCostUSD != nil
}

type AGDailyUsagePoint struct {
	DayKey              string
	Date                time.Time
	UncachedInputTokens int64
	CacheReadTokens     int64
	OutputTokens        int64
	RequestCount        int
	EstimatedCostUSD    float64
}

func (d AGDailyUsagePoint) TotalTokens() int64 {
	return d.UncachedInputTokens + d.CacheReadTokens + d.OutputTokens
}

type AGBillingPeriodInfo struct {
	Start         time.Time
	End           time.Time
	CurrentDay    int
	TotalDays     int
	RemainingDays int
}

type AGSpeedSample struct {
	ID               int
	Timestamp        time.Time
	TokensPerSec     float64
	OutputTokens     int64
	ModelDisplayName string
	TTFTMs           int64
	DurationMs       int64
	DecodeMs         int64
	IsExact          bool
}

type AGSpeedSnapshot struct {
	Recent5mTokPerSec  *float64
	Recent1hTokPerSec  *float64
	Recent1hReplyCount int
	LatestModelName    string
	Sparkline          []float64
	Samples            []AGSpeedSample
	IsRecentlyActive   bool
	LastResponseAgeSec *float64
	MedianTTFTMs       *int64
	IsExact            bool
}

func (s AGSpeedSnapshot) DisplayTokPerSec() *float64 {
	if s.Recent5mTokPerSec != nil {
		return s.Recent5mTokPerSec
	}
	return s.Recent1hTokPerSec
}

type AGContextEstimate struct {
	UsedTokens        int64
	ContextWindowSize int64
	UsedPercentage    float64
	Model             string
	ConversationID    string
	Timestamp         time.Time
}

type AGLocalUsageReport struct {
	Today              AGUsageWindowSummary
	FiveHour           AGUsageWindowSummary
	Week               AGUsageWindowSummary
	Month              AGUsageWindowSummary
	AllTime            AGUsageWindowSummary
	EarliestRecordDate *time.Time
	WeekPeriod         *AGBillingPeriodInfo
	MonthPeriod        *AGBillingPeriodInfo
	OutputSpeed        AGSpeedSnapshot
	LatestContext      *AGContextEstimate
	ByModelMonth       []AGModelUsageSummary
	ByModelWeek        []AGModelUsageSummary
	ByModelToday       []AGModelUsageSummary
	DailyTrend         []AGDailyUsagePoint
	ConversationCount  int
	TotalRecords       int
	UpdatedAt          time.Time
}

func (r AGLocalUsageReport) SummaryFor(span CostSpan) AGUsageWindowSummary {
	switch span {
	case SpanMonth:
		return r.Month
	case SpanDay:
		return r.Today
	default:
		return r.Week
	}
}

func (r AGLocalUsageReport) ModelsFor(span CostSpan) []AGModelUsageSummary {
	switch span {
	case SpanMonth:
		return r.ByModelMonth
	case SpanDay:
		return r.ByModelToday
	default:
		return r.ByModelWeek
	}
}

// --- Official StatusLine Snapshot ---

type AGStatusLineModelInfo struct {
	ID          string `json:"id,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Effort      string `json:"effort,omitempty"`
}

type AGStatusLineCurrentUsage struct {
	InputTokens              *int64 `json:"input_tokens,omitempty"`
	OutputTokens             *int64 `json:"output_tokens,omitempty"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens,omitempty"`
}

type AGStatusLineContextWindow struct {
	TotalInputTokens    *int64                    `json:"total_input_tokens,omitempty"`
	TotalOutputTokens   *int64                    `json:"total_output_tokens,omitempty"`
	ContextWindowSize   *int64                    `json:"context_window_size,omitempty"`
	UsedPercentage      *float64                  `json:"used_percentage,omitempty"`
	RemainingPercentage *float64                  `json:"remaining_percentage,omitempty"`
	CurrentUsage        *AGStatusLineCurrentUsage `json:"current_usage,omitempty"`
}

type AGStatusLineQuotaBucket struct {
	RemainingFraction *float64 `json:"remaining_fraction,omitempty"`
	RemainingAmount   *float64 `json:"remaining_amount,omitempty"`
	ResetTime         string   `json:"reset_time,omitempty"`
	ResetInSeconds    *int     `json:"reset_in_seconds,omitempty"`
	Disabled          *bool    `json:"disabled,omitempty"`
}

type AGStatusLineCostInfo struct {
	TotalUSD    *float64 `json:"total_usd,omitempty"`
	SubagentUSD *float64 `json:"subagent_usd,omitempty"`
	Estimated   *bool    `json:"estimated,omitempty"`
}

// AGFlexibleTime decodes and encodes ISO8601 strings or Unix timestamps (s/ms).
type AGFlexibleTime struct {
	T *time.Time
}

func (ft *AGFlexibleTime) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		ft.T = nil
		return nil
	}
	if s[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err == nil {
			if tm, ok := ParseAGISO8601(str); ok {
				ft.T = &tm
			}
		}
		return nil
	}
	var num float64
	if err := json.Unmarshal(b, &num); err == nil && num > 0 {
		if num > 1e12 {
			num /= 1000.0
		}
		sec, frac := math.Modf(num)
		tm := time.Unix(int64(sec), int64(math.Round(frac*1e9))).UTC()
		ft.T = &tm
	}
	return nil
}

func (ft AGFlexibleTime) MarshalJSON() ([]byte, error) {
	if ft.T == nil {
		return []byte("null"), nil
	}
	return json.Marshal(ft.T.UTC().Format(time.RFC3339Nano))
}

type AGStatusLineSnapshot struct {
	ConversationID    string                             `json:"conversation_id,omitempty"`
	ConversationTitle string                             `json:"conversation_title,omitempty"`
	Model             *AGStatusLineModelInfo             `json:"model,omitempty"`
	Version           string                             `json:"version,omitempty"`
	ContextWindow     *AGStatusLineContextWindow         `json:"context_window,omitempty"`
	Quota             map[string]AGStatusLineQuotaBucket `json:"quota,omitempty"`
	AgentState        string                             `json:"agent_state,omitempty"`
	PlanTier          string                             `json:"plan_tier,omitempty"`
	Email             string                             `json:"email,omitempty"`
	Cost              *AGStatusLineCostInfo              `json:"cost,omitempty"`
	CapturedAt        *AGFlexibleTime                    `json:"_captured_at,omitempty"`
}

func (s *AGStatusLineSnapshot) CapturedTime() *time.Time {
	if s == nil || s.CapturedAt == nil {
		return nil
	}
	return s.CapturedAt.T
}

// --- Evaluated Official Quota ---

type AGQuotaWindowKind string

const (
	AGWindowFiveHour AGQuotaWindowKind = "fiveHour"
	AGWindowWeekly   AGQuotaWindowKind = "weekly"
	AGWindowOther    AGQuotaWindowKind = "other"
)

func (k AGQuotaWindowKind) Duration() time.Duration {
	switch k {
	case AGWindowFiveHour:
		return 5 * time.Hour
	case AGWindowWeekly:
		return 7 * 24 * time.Hour
	default:
		return 0
	}
}

type AGQuotaBucketEval struct {
	BucketID            string
	DisplayName         string
	ShortName           string
	WindowKind          AGQuotaWindowKind
	UsedFraction        float64
	RemainingFraction   float64
	SafeLineFraction    *float64
	IsPaceSettling      bool
	ResetTime           *time.Time
	RemainingUntilReset *time.Duration
	IsResetElapsed      bool
	IsDisabled          bool
}

// UsedPercentInt returns the floored integer percentage (matching official rounding).
func (b AGQuotaBucketEval) UsedPercentInt() int {
	clamped := b.UsedFraction
	if math.IsNaN(clamped) || clamped < 0 {
		clamped = 0
	} else if clamped > 1 {
		clamped = 1
	}
	return int(math.Floor(clamped*100.0 + 1e-9))
}

func (b AGQuotaBucketEval) RemainingPercentInt() int {
	rem := 100 - b.UsedPercentInt()
	if rem < 0 {
		return 0
	}
	return rem
}

func (b AGQuotaBucketEval) SafeLinePercentInt() *int {
	if b.SafeLineFraction == nil {
		return nil
	}
	clamped := *b.SafeLineFraction
	if math.IsNaN(clamped) || clamped < 0 {
		clamped = 0
	} else if clamped > 1 {
		clamped = 1
	}
	v := int(math.Floor(clamped*100.0 + 1e-9))
	return &v
}

func (b AGQuotaBucketEval) IsOverPace() bool {
	if b.IsPaceSettling || b.IsResetElapsed {
		return false
	}
	safePct := b.SafeLinePercentInt()
	if safePct == nil {
		return false
	}
	return b.UsedPercentInt() > *safePct
}

type AGOfficialQuotaReport struct {
	Buckets            []AGQuotaBucketEval
	PrimaryFiveHour    *AGQuotaBucketEval
	PrimaryWeekly      *AGQuotaBucketEval
	SnapshotCapturedAt *time.Time
	IsStale            bool
	StaleAge           time.Duration
	ActiveModelName    string
	ContextWindow      *AGStatusLineContextWindow
	ConversationTitle  string
	AgentState         string
	PlanTier           string
	Email              string
	SessionCostUSD     *float64
}

func (r AGOfficialQuotaReport) HasSnapshot() bool {
	return r.SnapshotCapturedAt != nil
}

// ResolveAGContextWindow returns the active Antigravity context usage, preferring
// non-zero official statusLine context and falling back to the latest active
// local transcript turn when the background probe only returned an empty 0% state.
func ResolveAGContextWindow(off AGOfficialQuotaReport, local AGLocalUsageReport) (usedPct float64, usedTokens, ctxSize int64, ok bool) {
	if cw := off.ContextWindow; cw != nil {
		if cw.UsedPercentage != nil {
			usedPct = *cw.UsedPercentage
		}
		if cw.TotalInputTokens != nil && *cw.TotalInputTokens > 0 {
			usedTokens += *cw.TotalInputTokens
		}
		if cw.TotalOutputTokens != nil && *cw.TotalOutputTokens > 0 {
			usedTokens += *cw.TotalOutputTokens
		}
		if cw.ContextWindowSize != nil && *cw.ContextWindowSize > 0 {
			ctxSize = *cw.ContextWindowSize
		}
		if usedTokens > 0 || usedPct > 0 {
			if ctxSize <= 0 {
				ctxSize = AGContextWindowSizeFor(off.ActiveModelName)
			}
			if usedPct <= 0 && usedTokens > 0 && ctxSize > 0 {
				usedPct = float64(usedTokens) / float64(ctxSize) * 100.0
			}
			usedPct = max(0, min(100, usedPct))
			return usedPct, usedTokens, ctxSize, true
		}
	}
	if lc := local.LatestContext; lc != nil && lc.UsedTokens > 0 && lc.ContextWindowSize > 0 {
		pct := max(0, min(100, lc.UsedPercentage))
		return pct, lc.UsedTokens, lc.ContextWindowSize, true
	}
	return 0, 0, 0, false
}
