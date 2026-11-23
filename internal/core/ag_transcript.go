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

type rawAGTranscriptLine struct {
	CreatedAt       string `json:"created_at"`
	Model           string `json:"model"`
	InputTokens     *int64 `json:"input_tokens"`
	CacheReadTokens *int64 `json:"cache_read_tokens"`
	OutputTokens    *int64 `json:"output_tokens"`
}

func ParseAGTranscriptLine(line []byte, conversationID, fallbackModel string) (AGTranscriptRecord, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return AGTranscriptRecord{}, false
	}
	var raw rawAGTranscriptLine
	if err := json.Unmarshal(line, &raw); err != nil {
		return AGTranscriptRecord{}, false
	}
	if raw.InputTokens == nil && raw.OutputTokens == nil && raw.CacheReadTokens == nil {
		return AGTranscriptRecord{}, false
	}
	ts, ok := ParseAGISO8601(raw.CreatedAt)
	if !ok {
		return AGTranscriptRecord{}, false
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

	return AGTranscriptRecord{
		Timestamp:       ts,
		Model:           resolved,
		InputTokens:     inTok,
		CacheReadTokens: cacheTok,
		OutputTokens:    outTok,
		ConversationID:  conversationID,
	}, true
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
	var results []AGTranscriptRecord
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
		if rec, ok := ParseAGTranscriptLine(slice, conversationID, known); ok {
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
	return results
}

type agFileCursor struct {
	byteOffset     int64
	modTime        time.Time
	records        []AGTranscriptRecord
	lastKnownModel string
}

// AGTranscriptIndex incrementally indexes ~/.gemini/antigravity/brain/<id>/.system_generated/logs/transcript.jsonl.
type AGTranscriptIndex struct {
	mu       sync.Mutex
	brainDir string
	cursors  map[string]*agFileCursor
}

func NewAGTranscriptIndex(brainDir string) *AGTranscriptIndex {
	return &AGTranscriptIndex{
		brainDir: brainDir,
		cursors:  map[string]*agFileCursor{},
	}
}

func (idx *AGTranscriptIndex) Reset() {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.cursors = map[string]*agFileCursor{}
}

func (idx *AGTranscriptIndex) SetBrainDir(brainDir string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if !strings.EqualFold(idx.brainDir, brainDir) {
		idx.brainDir = brainDir
		idx.cursors = map[string]*agFileCursor{}
	}
}

func (idx *AGTranscriptIndex) Refresh() (records []AGTranscriptRecord, conversationCount int) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if idx.brainDir == "" {
		idx.cursors = map[string]*agFileCursor{}
		return nil, 0
	}
	entries, err := os.ReadDir(idx.brainDir)
	if err != nil {
		idx.cursors = map[string]*agFileCursor{}
		return nil, 0
	}

	activePaths := make(map[string]bool, len(entries))
	convIDs := make(map[string]bool, len(entries))

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
			recs, newOff := readAGTranscriptSlice(transcriptPath, 0, name, &lastModel)
			cur = &agFileCursor{
				byteOffset:     newOff,
				modTime:        modTime,
				records:        recs,
				lastKnownModel: lastModel,
			}
			idx.cursors[transcriptPath] = cur
		} else if fileSize < cur.byteOffset {
			var lastModel string
			recs, newOff := readAGTranscriptSlice(transcriptPath, 0, name, &lastModel)
			cur.byteOffset = newOff
			cur.modTime = modTime
			cur.records = recs
			cur.lastKnownModel = lastModel
		} else if fileSize > cur.byteOffset || modTime.After(cur.modTime) {
			lastModel := cur.lastKnownModel
			recs, newOff := readAGTranscriptSlice(transcriptPath, cur.byteOffset, name, &lastModel)
			cur.byteOffset = newOff
			cur.modTime = modTime
			cur.lastKnownModel = lastModel
			if len(recs) > 0 {
				cur.records = append(cur.records, recs...)
			}
		}

		if len(cur.records) > 0 {
			convIDs[name] = true
			records = append(records, cur.records...)
		}
	}

	for path := range idx.cursors {
		if !activePaths[path] {
			delete(idx.cursors, path)
		}
	}

	return records, len(convIDs)
}

func readAGTranscriptSlice(path string, offset int64, conversationID string, lastKnownModel *string) ([]AGTranscriptRecord, int64) {
	f, err := os.Open(path)
	if err != nil {
		return nil, offset
	}
	defer f.Close()

	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil, offset
		}
	}
	data, err := io.ReadAll(f)
	if err != nil || len(data) == 0 {
		return nil, offset
	}

	lastNL := bytes.LastIndexByte(data, '\n')
	if lastNL >= 0 {
		completeLen := lastNL + 1
		recs := ParseAGTranscriptBytes(data[:completeLen], conversationID, lastKnownModel)
		return recs, offset + int64(completeLen)
	}

	var fallback string
	if lastKnownModel != nil {
		fallback = *lastKnownModel
	}
	if rec, ok := ParseAGTranscriptLine(data, conversationID, fallback); ok {
		if lastKnownModel != nil && !strings.EqualFold(rec.Model, "unknown") {
			*lastKnownModel = rec.Model
		}
		return []AGTranscriptRecord{rec}, offset + int64(len(data))
	}
	return nil, offset
}
