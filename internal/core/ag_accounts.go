package core

import (
	"strings"
	"time"
)

// NormalizeAGEmail canonicalizes an Antigravity account email for comparisons and map keys.
func NormalizeAGEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// AGAccountSegment records a contiguous time span [Start, End) during which Email was active.
// A nil End indicates the currently active segment.
type AGAccountSegment struct {
	Email string     `json:"email"`
	Start time.Time  `json:"start"`
	End   *time.Time `json:"end,omitempty"`
}

// AGAccountPersistedState holds per-account persistent metadata such as full-week spend basis.
type AGAccountPersistedState struct {
	FullWeekCostUSD float64 `json:"fullWeekCostUsd,omitempty"`
	FullWeekTokens  int64   `json:"fullWeekTokens,omitempty"`
}

// AGAccountTimeline tracks when Antigravity accounts became active on this machine so
// shared local transcripts (which lack email tags) can be attributed by timestamp.
type AGAccountTimeline struct {
	Segments    []AGAccountSegment `json:"segments,omitempty"`
	HasSwitched bool               `json:"hasSwitched,omitempty"`
}

const (
	agTimelineRetention = 60 * 24 * time.Hour
	agTimelineMinKeep   = 50
)

// Observe records email as active at `at`, closing any previous active segment if switched.
// Returns changed=true if the timeline state was mutated, and switched=true if the active account changed.
func (t *AGAccountTimeline) Observe(email string, at time.Time) (changed bool, switched bool) {
	norm := NormalizeAGEmail(email)
	if norm == "" {
		return false, false
	}
	tUTC := at.UTC()
	if len(t.Segments) == 0 {
		t.Segments = append(t.Segments, AGAccountSegment{
			Email: norm,
			Start: tUTC,
		})
		return true, false
	}

	last := &t.Segments[len(t.Segments)-1]
	if NormalizeAGEmail(last.Email) == norm && last.End == nil {
		return false, false
	}

	if tUTC.Before(last.Start) {
		tUTC = last.Start
	}
	endCopy := tUTC
	last.End = &endCopy
	t.Segments = append(t.Segments, AGAccountSegment{
		Email: norm,
		Start: tUTC,
	})
	t.HasSwitched = true
	t.prune(tUTC)
	return true, true
}

func (t *AGAccountTimeline) prune(nowUTC time.Time) {
	if len(t.Segments) <= agTimelineMinKeep {
		return
	}
	cutoff := nowUTC.Add(-agTimelineRetention)
	dropCount := 0
	maxDrop := len(t.Segments) - agTimelineMinKeep
	for dropCount < maxDrop {
		seg := t.Segments[dropCount]
		if seg.End == nil || !seg.End.Before(cutoff) {
			break
		}
		dropCount++
	}
	if dropCount > 0 {
		t.Segments = append([]AGAccountSegment(nil), t.Segments[dropCount:]...)
	}
}

// HasMultipleAccounts reports whether more than one Antigravity account has ever been observed.
func (t AGAccountTimeline) HasMultipleAccounts() bool {
	return t.HasSwitched || len(t.Segments) > 1
}

// AccountAt returns the normalized email active at timestamp `ts`, or "" if unattributed.
func (t AGAccountTimeline) AccountAt(ts time.Time) string {
	if len(t.Segments) == 0 {
		return ""
	}
	u := ts.UTC()
	if u.Before(t.Segments[0].Start) {
		return ""
	}
	for i := len(t.Segments) - 1; i >= 0; i-- {
		seg := t.Segments[i]
		if !u.Before(seg.Start) && (seg.End == nil || u.Before(*seg.End)) {
			return NormalizeAGEmail(seg.Email)
		}
	}
	return ""
}

// FilterRecords returns transcript records belonging to activeEmail according to the timeline.
// Before any account switch is recorded, or when includeAll is true, all records pass through unfiltered.
func (t AGAccountTimeline) FilterRecords(records []AGTranscriptRecord, activeEmail string, includeAll bool) (filtered []AGTranscriptRecord, convCount int, isFiltered bool) {
	email := NormalizeAGEmail(activeEmail)
	if includeAll || email == "" || !t.HasMultipleAccounts() {
		return records, countAGConversations(records), false
	}
	out := make([]AGTranscriptRecord, 0, len(records))
	for _, r := range records {
		if t.AccountAt(r.Timestamp) == email {
			out = append(out, r)
		}
	}
	return out, countAGConversations(out), true
}

func countAGConversations(recs []AGTranscriptRecord) int {
	if len(recs) == 0 {
		return 0
	}
	seen := make(map[string]struct{}, len(recs))
	for _, r := range recs {
		if r.ConversationID != "" {
			seen[r.ConversationID] = struct{}{}
		}
	}
	return len(seen)
}
