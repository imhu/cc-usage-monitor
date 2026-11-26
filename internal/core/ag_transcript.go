package core

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ParseAGISO8601 parses RFC3339/ISO8601 strings (with or without fractional seconds).
func ParseAGISO8601(s string) (time.Time, bool) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return time.Time{}, false
	}
	if ts, ok := ParseTimestamp(trimmed); ok {
		sec, frac := math.Modf(ts)
		ns := int64(math.Round(frac * 1e9))
		if ns >= 1e9 {
			sec += 1
			ns -= 1e9
		}
		return time.Unix(int64(sec), ns).UTC(), true
	}
	if t, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
		return t.UTC(), true
	}
	if t, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return t.UTC(), true
	}
	return time.Time{}, false
}

type rawAGToolCallRef struct {
	Name string `json:"name"`
}

type rawAGTranscriptLine struct {
	StepIndex       *int               `json:"step_index"`
	Source          string             `json:"source"`
	Type            string             `json:"type"`
	Status          string             `json:"status"`
	CreatedAt       string             `json:"created_at"`
	Model           string             `json:"model"`
	InputTokens     *int64             `json:"input_tokens"`
	CacheReadTokens *int64             `json:"cache_read_tokens"`
	OutputTokens    *int64             `json:"output_tokens"`
	ToolCalls       []rawAGToolCallRef `json:"tool_calls"`
}

type agTranscriptEvent struct {
	Timestamp   time.Time
	Type        string
	Status      string
	HasToolCall bool
	WaitingUser bool
}

func ParseAGTranscriptLine(line []byte, conversationID, fallbackModel string) (AGTranscriptRecord, bool) {
	rec, hasRec, _, _ := parseAGTranscriptLineFull(line, conversationID, fallbackModel)
	return rec, hasRec
}

func parseAGTranscriptLineFull(line []byte, conversationID, fallbackModel string) (rec AGTranscriptRecord, hasRec bool, ev agTranscriptEvent, hasEv bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return AGTranscriptRecord{}, false, agTranscriptEvent{}, false
	}
	var raw rawAGTranscriptLine
	if err := json.Unmarshal(line, &raw); err != nil {
		return AGTranscriptRecord{}, false, agTranscriptEvent{}, false
	}

	ts, ok := ParseAGISO8601(raw.CreatedAt)
	if raw.Type != "" || raw.Status != "" || ok {
		waitingUser := false
		if len(raw.ToolCalls) > 0 {
			waitingUser = true
			for _, tc := range raw.ToolCalls {
				if !strings.EqualFold(strings.TrimSpace(tc.Name), "ask_question") {
					waitingUser = false
					break
				}
			}
		}
		ev = agTranscriptEvent{
			Timestamp:   ts,
			Type:        raw.Type,
			Status:      raw.Status,
			HasToolCall: len(raw.ToolCalls) > 0,
			WaitingUser: waitingUser,
		}
		hasEv = true
	}

	if raw.InputTokens == nil && raw.OutputTokens == nil && raw.CacheReadTokens == nil {
		return AGTranscriptRecord{}, false, ev, hasEv
	}
	if !ok {
		return AGTranscriptRecord{}, false, ev, hasEv
	}
	resolved := resolveAGModelName(raw.Model, fallbackModel)

	var inTok, cacheTok, outTok int64
	if raw.InputTokens != nil && *raw.InputTokens > 0 {
		inTok = *raw.InputTokens
	}
	if raw.CacheReadTokens != nil && *raw.CacheReadTokens > 0 {
		cacheTok = *raw.CacheReadTokens
	}
	if raw.OutputTokens != nil && *raw.OutputTokens > 0 {
		outTok = *raw.OutputTokens
	}

	var stepIdx int
	var hasStep bool
	if raw.StepIndex != nil && *raw.StepIndex >= 0 {
		stepIdx = *raw.StepIndex
		hasStep = true
	}

	return AGTranscriptRecord{
		Timestamp:       ts,
		Model:           resolved,
		InputTokens:     inTok,
		CacheReadTokens: cacheTok,
		OutputTokens:    outTok,
		ConversationID:  conversationID,
		StepIndex:       stepIdx,
		HasStepIndex:    hasStep,
	}, true, ev, hasEv
}

func resolveAGModelName(explicit, fallback string) string {
	ex := strings.TrimSpace(explicit)
	if ex != "" && !strings.EqualFold(ex, "unknown") {
		return ex
	}
	fb := strings.TrimSpace(fallback)
	if fb != "" && !strings.EqualFold(fb, "unknown") {
		return fb
	}
	return "unknown"
}

func ParseAGTranscriptBytes(data []byte, conversationID string, lastKnownModel *string) []AGTranscriptRecord {
	recs, _ := parseAGTranscriptBytesFull(data, conversationID, lastKnownModel)
	return recs
}

func parseAGTranscriptBytesFull(data []byte, conversationID string, lastKnownModel *string) ([]AGTranscriptRecord, *agTranscriptEvent) {
	var results []AGTranscriptRecord
	var lastEvent *agTranscriptEvent
	var known, firstKnown string
	if lastKnownModel != nil {
		known = *lastKnownModel
	}
	hasLeadingUnknown := false

	start := 0
	for start < len(data) {
		idx := bytes.IndexByte(data[start:], '\n')
		var slice []byte
		if idx < 0 {
			slice = data[start:]
			start = len(data)
		} else {
			slice = data[start : start+idx]
			start += idx + 1
		}
		rec, hasRec, ev, hasEv := parseAGTranscriptLineFull(slice, conversationID, known)
		if hasEv {
			evCopy := ev
			lastEvent = &evCopy
		}
		if hasRec {
			if !strings.EqualFold(rec.Model, "unknown") {
				if firstKnown == "" {
					firstKnown = rec.Model
				}
				known = rec.Model
			} else {
				hasLeadingUnknown = true
			}
			results = append(results, rec)
		}
	}

	if hasLeadingUnknown && firstKnown != "" {
		for i := range results {
			if strings.EqualFold(results[i].Model, "unknown") {
				results[i].Model = firstKnown
			}
		}
	}
	if lastKnownModel != nil {
		*lastKnownModel = known
	}
	return results, lastEvent
}

type agFileCursor struct {
	conversationID string
	byteOffset     int64
	modTime        time.Time
	records        []AGTranscriptRecord
	lastKnownModel string
	lastEvent      *agTranscriptEvent
}

type agTimingCacheEntry struct {
	dbSize  int64
	dbMod   time.Time
	walSize int64
	walMod  time.Time
	timings map[int]AGStepTiming
}

// AGTranscriptIndex incrementally indexes ~/.gemini/antigravity/brain/<id>/.system_generated/logs/transcript.jsonl.
type AGTranscriptIndex struct {
	mu          sync.Mutex
	brainDir    string
	cursors     map[string]*agFileCursor
	timingCache map[string]*agTimingCacheEntry
}

func NewAGTranscriptIndex(brainDir string) *AGTranscriptIndex {
	return &AGTranscriptIndex{
		brainDir:    brainDir,
		cursors:     map[string]*agFileCursor{},
		timingCache: map[string]*agTimingCacheEntry{},
	}
}

func (idx *AGTranscriptIndex) Reset() {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.cursors = map[string]*agFileCursor{}
	idx.timingCache = map[string]*agTimingCacheEntry{}
}

func (idx *AGTranscriptIndex) SetBrainDir(brainDir string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if !strings.EqualFold(idx.brainDir, brainDir) {
		idx.brainDir = brainDir
		idx.cursors = map[string]*agFileCursor{}
		idx.timingCache = map[string]*agTimingCacheEntry{}
	}
}

func (idx *AGTranscriptIndex) Refresh() (records []AGTranscriptRecord, conversationCount int) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if idx.brainDir == "" {
		idx.cursors = map[string]*agFileCursor{}
		idx.timingCache = map[string]*agTimingCacheEntry{}
		return nil, 0
	}
	entries, err := os.ReadDir(idx.brainDir)
	if err != nil {
		idx.cursors = map[string]*agFileCursor{}
		idx.timingCache = map[string]*agTimingCacheEntry{}
		return nil, 0
	}

	if idx.timingCache == nil {
		idx.timingCache = map[string]*agTimingCacheEntry{}
	}
	convDir := filepath.Join(filepath.Dir(idx.brainDir), "conversations")
	recentCutoff := time.Now().Add(-2 * time.Hour)
	activePaths := make(map[string]bool, len(entries))
	convIDs := make(map[string]bool, len(entries))
	recentConvs := make(map[string]bool)

	for _, ent := range entries {
		name := ent.Name()
		if strings.HasPrefix(name, ".") || !ent.IsDir() {
			continue
		}
		transcriptPath := filepath.Join(idx.brainDir, name, ".system_generated", "logs", "transcript.jsonl")
		info, err := os.Stat(transcriptPath)
		if err != nil || info.IsDir() {
			continue
		}
		activePaths[transcriptPath] = true
		fileSize := info.Size()
		modTime := info.ModTime()

		cur, ok := idx.cursors[transcriptPath]
		if !ok {
			var lastModel string
			recs, newOff, lastEv := readAGTranscriptSlice(transcriptPath, 0, name, &lastModel)
			cur = &agFileCursor{
				conversationID: name,
				byteOffset:     newOff,
				modTime:        modTime,
				records:        recs,
				lastKnownModel: lastModel,
				lastEvent:      lastEv,
			}
			idx.cursors[transcriptPath] = cur
		} else if fileSize < cur.byteOffset {
			var lastModel string
			recs, newOff, lastEv := readAGTranscriptSlice(transcriptPath, 0, name, &lastModel)
			cur.conversationID = name
			cur.byteOffset = newOff
			cur.modTime = modTime
			cur.records = recs
			cur.lastKnownModel = lastModel
			cur.lastEvent = lastEv
		} else if fileSize > cur.byteOffset || modTime.After(cur.modTime) {
			lastModel := cur.lastKnownModel
			recs, newOff, lastEv := readAGTranscriptSlice(transcriptPath, cur.byteOffset, name, &lastModel)
			cur.conversationID = name
			cur.byteOffset = newOff
			cur.modTime = modTime
			cur.lastKnownModel = lastModel
			if lastEv != nil {
				cur.lastEvent = lastEv
			}
			if len(recs) > 0 {
				cur.records = append(cur.records, recs...)
			}
		}

		if len(cur.records) > 0 {
			convIDs[name] = true
			var timings map[int]AGStepTiming
			if agHasRecentRecord(cur.records, recentCutoff) {
				recentConvs[name] = true
				timings = idx.stepTimingsForLocked(convDir, name)
			}
			if len(timings) > 0 {
				for _, rec := range cur.records {
					if rec.HasStepIndex {
						if tm, found := timings[rec.StepIndex]; found {
							rec.TTFTMs = tm.TTFTMs
							rec.DurationMs = tm.DurationMs
							rec.DecodeMs = tm.DecodeMs
						}
					}
					records = append(records, rec)
				}
			} else {
				records = append(records, cur.records...)
			}
		}
	}

	for path := range idx.cursors {
		if !activePaths[path] {
			delete(idx.cursors, path)
		}
	}
	for convID := range idx.timingCache {
		if !recentConvs[convID] {
			delete(idx.timingCache, convID)
		}
	}

	return records, len(convIDs)
}

func agHasRecentRecord(recs []AGTranscriptRecord, cutoff time.Time) bool {
	for i := len(recs) - 1; i >= 0; i-- {
		if !recs[i].Timestamp.Before(cutoff) {
			return true
		}
	}
	return false
}

func (idx *AGTranscriptIndex) stepTimingsForLocked(convDir, convID string) map[int]AGStepTiming {
	if convDir == "" || convID == "" {
		return nil
	}
	dbPath := filepath.Join(convDir, convID+".db")
	dbInfo, err := os.Stat(dbPath)
	if err != nil || dbInfo.IsDir() {
		return nil
	}
	var walSize int64
	var walMod time.Time
	if walInfo, err := os.Stat(dbPath + "-wal"); err == nil && !walInfo.IsDir() {
		walSize = walInfo.Size()
		walMod = walInfo.ModTime()
	}

	if cached, ok := idx.timingCache[convID]; ok &&
		cached.dbSize == dbInfo.Size() && cached.dbMod.Equal(dbInfo.ModTime()) &&
		cached.walSize == walSize && cached.walMod.Equal(walMod) {
		return cached.timings
	}

	timings, err := ReadAGConversationStepTimings(dbPath)
	if err != nil {
		if cached, ok := idx.timingCache[convID]; ok {
			return cached.timings
		}
		return nil
	}
	idx.timingCache[convID] = &agTimingCacheEntry{
		dbSize:  dbInfo.Size(),
		dbMod:   dbInfo.ModTime(),
		walSize: walSize,
		walMod:  walMod,
		timings: timings,
	}
	return timings
}

// ExecutionState inspects the latest transcript step across all indexed conversations
// to determine whether Antigravity is currently executing an AI turn.
func (idx *AGTranscriptIndex) ExecutionState(now time.Time) AGExecutionState {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	var best AGExecutionState
	for _, cur := range idx.cursors {
		if cur.lastEvent == nil {
			continue
		}
		cand := evaluateAGTranscriptEvent(cur.conversationID, *cur.lastEvent, cur.modTime, now)
		if !cand.IsExecuting {
			continue
		}
		if !best.IsExecuting || cand.ActiveUntil.After(best.ActiveUntil) ||
			(cand.ActiveUntil.Equal(best.ActiveUntil) && cand.LastEventAt.After(best.LastEventAt)) {
			best = cand
		}
	}
	return best
}

func evaluateAGTranscriptEvent(convID string, ev agTranscriptEvent, fileMod, now time.Time) AGExecutionState {
	eventAt := ev.Timestamp
	if eventAt.IsZero() {
		eventAt = fileMod
	} else if fileMod.After(eventAt) && fileMod.Sub(eventAt) <= 10*time.Minute {
		eventAt = fileMod
	}
	if eventAt.After(now) {
		eventAt = now
	}

	typ := strings.ToUpper(strings.TrimSpace(ev.Type))
	st := strings.ToUpper(strings.TrimSpace(ev.Status))

	switch st {
	case "CANCELLED", "CANCELED", "ERROR", "FAILED", "ABORTED", "STOPPED", "WAITING", "WAITING_FOR_USER", "BLOCKED", "PAUSED":
		return AGExecutionState{}
	}

	var activeUntil time.Time
	switch st {
	case "RUNNING", "IN_PROGRESS", "STREAMING", "EXECUTING", "PENDING":
		activeUntil = eventAt.Add(5 * time.Minute)
	default:
		switch typ {
		case "USER_INPUT":
			activeUntil = eventAt.Add(3 * time.Minute)
		case "PLANNER_RESPONSE":
			if ev.HasToolCall && !ev.WaitingUser {
				activeUntil = eventAt.Add(3 * time.Minute)
			} else {
				return AGExecutionState{}
			}
		case "GENERIC":
			activeUntil = eventAt.Add(3 * time.Minute)
		case "SYSTEM_MESSAGE":
			activeUntil = eventAt.Add(2 * time.Minute)
		default:
			return AGExecutionState{}
		}
	}

	if !now.Before(activeUntil) {
		return AGExecutionState{}
	}
	return AGExecutionState{
		IsExecuting:    true,
		ActiveUntil:    activeUntil,
		LastEventAt:    eventAt,
		ConversationID: convID,
	}
}

func readAGTranscriptSlice(path string, offset int64, conversationID string, lastKnownModel *string) ([]AGTranscriptRecord, int64, *agTranscriptEvent) {
	f, err := os.Open(path)
	if err != nil {
		return nil, offset, nil
	}
	defer f.Close()

	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil, offset, nil
		}
	}
	data, err := io.ReadAll(f)
	if err != nil || len(data) == 0 {
		return nil, offset, nil
	}

	lastNL := bytes.LastIndexByte(data, '\n')
	if lastNL >= 0 {
		completeLen := lastNL + 1
		recs, lastEv := parseAGTranscriptBytesFull(data[:completeLen], conversationID, lastKnownModel)
		return recs, offset + int64(completeLen), lastEv
	}

	var fallback string
	if lastKnownModel != nil {
		fallback = *lastKnownModel
	}
	rec, hasRec, ev, hasEv := parseAGTranscriptLineFull(data, conversationID, fallback)
	var lastEv *agTranscriptEvent
	if hasEv {
		evCopy := ev
		lastEv = &evCopy
	}
	if hasRec {
		if lastKnownModel != nil && !strings.EqualFold(rec.Model, "unknown") {
			*lastKnownModel = rec.Model
		}
		return []AGTranscriptRecord{rec}, offset + int64(len(data)), lastEv
	}
	return nil, offset, nil
}
