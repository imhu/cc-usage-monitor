package core

import (
	"encoding/gob"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type UsageRecord struct {
	Key         uint64
	Time        float64
	ModelIdx    uint16
	Tokens      TokenCounts
	WebSearches uint16
	IsFast      bool
	IsSidechain bool
}

type FileCursor struct {
	Offset int64
	Size   int64
	Mtime  int64
}

type RequestTiming struct {
	Key         uint64
	Start       float64
	End         float64
	Output      int64
	ModelIdx    uint16
	IsFast      bool
	IsSidechain bool
}

type ScanStats struct {
	FilesScanned   int
	RecordsChanged bool
	TimingsChanged bool
}

const TimingRetention = 2 * time.Hour

type UsageIndex struct {
	Models        []string
	modelToIdx    map[string]uint16
	Records       []UsageRecord
	keyToIdx      map[uint64]int
	Cursors       map[string]FileCursor
	Timings       map[uint64]RequestTiming
	Starts        map[string]RequestStart // api-request id -> start (retained for TimingRetention)
	Pending       map[string]RequestStart // file path -> in-flight request for that session file
	LastUntimed   float64
	Dirty         bool
	timingsPrimed bool
}

func NewUsageIndex() *UsageIndex {
	return &UsageIndex{
		modelToIdx: map[string]uint16{},
		keyToIdx:   map[uint64]int{},
		Cursors:    map[string]FileCursor{},
		Timings:    map[uint64]RequestTiming{},
		Starts:     map[string]RequestStart{},
		Pending:    map[string]RequestStart{},
	}
}

func (idx *UsageIndex) internModel(m string) uint16 {
	if i, ok := idx.modelToIdx[m]; ok {
		return i
	}
	i := uint16(len(idx.Models))
	idx.Models = append(idx.Models, m)
	idx.modelToIdx[m] = i
	return i
}

func (idx *UsageIndex) rebuildMaps() {
	idx.modelToIdx = make(map[string]uint16, len(idx.Models))
	for i, m := range idx.Models {
		idx.modelToIdx[m] = uint16(i)
	}
	idx.keyToIdx = make(map[uint64]int, len(idx.Records))
	for i, r := range idx.Records {
		idx.keyToIdx[r.Key] = i
	}
	if idx.Cursors == nil {
		idx.Cursors = map[string]FileCursor{}
	}
	if idx.Timings == nil {
		idx.Timings = map[uint64]RequestTiming{}
	}
	if idx.Starts == nil {
		idx.Starts = map[string]RequestStart{}
	}
	if idx.Pending == nil {
		idx.Pending = map[string]RequestStart{}
	}
}

func (idx *UsageIndex) ingestEvents(events []ParsedEvent, filePath string) (recordsChanged bool, timingsChanged bool) {
	if idx.Starts == nil {
		idx.Starts = map[string]RequestStart{}
	}
	if idx.Pending == nil {
		idx.Pending = map[string]RequestStart{}
	}
	for _, ev := range events {
		switch ev.Kind {
		case EventRequestStarted:
			idx.Starts[ev.Start.ID] = ev.Start
			pendingKey := filePath
			if pendingKey == "" {
				pendingKey = ev.Start.ID
			}
			idx.Pending[pendingKey] = ev.Start
			timingsChanged = true
		case EventRequestEnded:
			if filePath != "" {
				if _, ok := idx.Pending[filePath]; ok {
					delete(idx.Pending, filePath)
					timingsChanged = true
				}
			}
		case EventUsage:
			u := ev.Usage
			mi := idx.internModel(u.Model)
			rec := UsageRecord{
				Key:         u.Key,
				Time:        u.Time,
				ModelIdx:    mi,
				Tokens:      u.Tokens,
				WebSearches: uint16(min(u.WebSearches, 65535)),
				IsFast:      u.IsFast,
				IsSidechain: u.IsSidechain,
			}
			if pos, exists := idx.keyToIdx[u.Key]; exists {
				old := idx.Records[pos]
				// Streaming writes multiple rows per message; keep the row with largest output
				// (tie-breaking on prompt size) and earliest timestamp, matching UsageIndex.swift.
				better := rec.Tokens.Output > old.Tokens.Output ||
					(rec.Tokens.Output == old.Tokens.Output && rec.Tokens.Prompt() > old.Tokens.Prompt())
				if better {
					if old.Time < rec.Time {
						rec.Time = old.Time
					}
					idx.Records[pos] = rec
					recordsChanged = true
				}
			} else {
				idx.keyToIdx[u.Key] = len(idx.Records)
				idx.Records = append(idx.Records, rec)
				recordsChanged = true
			}
			if idx.recordTiming(u, mi, filePath) {
				timingsChanged = true
			}
		}
	}
	return recordsChanged, timingsChanged
}

// recordTiming matches UsageIndex.swift recordTiming(_:file:) 1:1:
// keeps Starts intact across streaming chunks, clears per-file Pending once the
// matching reply arrives, and updates End/Output monotonically across chunks.
func (idx *UsageIndex) recordTiming(u ParsedUsage, mi uint16, filePath string) bool {
	if u.RequestRef == "" {
		if u.Time > idx.LastUntimed {
			idx.LastUntimed = u.Time
		}
		return false
	}
	changed := false
	if filePath != "" {
		if p, ok := idx.Pending[filePath]; ok && p.ID == u.RequestRef {
			delete(idx.Pending, filePath)
			changed = true
		}
	} else if _, ok := idx.Pending[u.RequestRef]; ok {
		delete(idx.Pending, u.RequestRef)
		changed = true
	}

	start, ok := idx.Starts[u.RequestRef]
	if !ok || u.Time < start.Time {
		return changed
	}
	if existing, ok := idx.Timings[u.Key]; ok {
		end := max(existing.End, u.Time)
		out := max(existing.Output, u.Tokens.Output)
		if end == existing.End && out == existing.Output {
			return changed
		}
		existing.End = end
		existing.Output = out
		idx.Timings[u.Key] = existing
		return true
	}
	idx.Timings[u.Key] = RequestTiming{
		Key:         u.Key,
		Start:       start.Time,
		End:         u.Time,
		Output:      u.Tokens.Output,
		ModelIdx:    mi,
		IsFast:      u.IsFast,
		IsSidechain: u.IsSidechain,
	}
	return true
}

// ScanRoots incrementally scans all *.jsonl files under roots.
// On the first scan after launch, recently modified files (within TimingRetention)
// are re-read from offset 0 to prime Starts, Pending, and Timings cleanly.
func (idx *UsageIndex) ScanRoots(roots []string, onProgress func(float64)) ScanStats {
	type fileEntry struct {
		path  string
		size  int64
		mtime int64
	}
	var files []fileEntry
	seen := map[string]bool{}
	for _, root := range roots {
		if root == "" {
			continue
		}
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(strings.ToLower(d.Name()), ".jsonl") {
				return nil
			}
			if seen[p] {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			seen[p] = true
			files = append(files, fileEntry{path: p, size: info.Size(), mtime: info.ModTime().UnixNano()})
			return nil
		})
	}

	var stats ScanStats
	now := time.Now()
	primeSince := int64(1<<62 - 1)
	if !idx.timingsPrimed {
		primeSince = now.Add(-TimingRetention).UnixNano()
		idx.timingsPrimed = true
	}

	type scanJob struct {
		file   fileEntry
		offset int64
	}
	var jobs []scanJob
	for _, f := range files {
		if f.mtime >= primeSince {
			jobs = append(jobs, scanJob{file: f, offset: 0})
		} else if cur, ok := idx.Cursors[f.path]; ok {
			if cur.Size == f.size && cur.Mtime == f.mtime && cur.Offset <= f.size {
				continue
			}
			if f.size >= cur.Offset {
				jobs = append(jobs, scanJob{file: f, offset: cur.Offset})
			} else {
				// File truncated or replaced; re-read from start.
				jobs = append(jobs, scanJob{file: f, offset: 0})
			}
		} else {
			jobs = append(jobs, scanJob{file: f, offset: 0})
		}
	}

	// Clean cursors and pending requests for deleted files (keep historical records).
	for path := range idx.Cursors {
		if !seen[path] {
			delete(idx.Cursors, path)
		}
	}
	for path := range idx.Pending {
		if !seen[path] {
			delete(idx.Pending, path)
			stats.TimingsChanged = true
		}
	}

	total := len(jobs)
	for step, job := range jobs {
		f := job.file
		res := ParseJSONLFile(f.path, job.offset)
		recChg, timChg := idx.ingestEvents(res.Events, f.path)
		if recChg {
			stats.RecordsChanged = true
		}
		if timChg {
			stats.TimingsChanged = true
		}
		idx.Cursors[f.path] = FileCursor{
			Offset: res.EndOffset,
			Size:   f.size,
			Mtime:  f.mtime,
		}
		stats.FilesScanned++
		if onProgress != nil && total > 0 && (step%8 == 0 || step == total-1) {
			onProgress(float64(step+1) / float64(total))
		}
	}

	// Prune stale timings, starts, and pending older than TimingRetention (2 hours).
	cutoff := unixSec(now.Add(-TimingRetention))
	for k, t := range idx.Timings {
		if t.End < cutoff {
			delete(idx.Timings, k)
		}
	}
	for k, s := range idx.Starts {
		if s.Time < cutoff {
			delete(idx.Starts, k)
		}
	}
	for k, p := range idx.Pending {
		if p.Time < cutoff {
			delete(idx.Pending, k)
		}
	}

	if stats.RecordsChanged {
		idx.Dirty = true
	}
	return stats
}

type IndexSnapshot struct {
	Records []UsageRecord
	Models  []string
}

func (idx *UsageIndex) MakeSnapshot() IndexSnapshot {
	recs := make([]UsageRecord, len(idx.Records))
	copy(recs, idx.Records)
	sort.Slice(recs, func(i, j int) bool { return recs[i].Time < recs[j].Time })
	models := make([]string, len(idx.Models))
	copy(models, idx.Models)
	return IndexSnapshot{Records: recs, Models: models}
}

func (idx *UsageIndex) SpeedLog() SpeedLog {
	reqs := make([]TimedRequest, 0, len(idx.Timings))
	for _, t := range idx.Timings {
		name := "Other"
		if int(t.ModelIdx) < len(idx.Models) {
			name = ModelInfoFor(idx.Models[t.ModelIdx]).DisplayName
		}
		reqs = append(reqs, TimedRequest{
			ID:          t.Key,
			Model:       name,
			IsFast:      t.IsFast,
			Start:       t.Start,
			End:         t.End,
			Output:      int(t.Output),
			IsSidechain: t.IsSidechain,
		})
	}
	sort.Slice(reqs, func(i, j int) bool {
		if reqs[i].End != reqs[j].End {
			return reqs[i].End < reqs[j].End
		}
		return reqs[i].ID < reqs[j].ID
	})
	waiting := make([]PendingRequest, 0, len(idx.Pending))
	for _, p := range idx.Pending {
		modelName := ""
		if p.Model != "" {
			modelName = ModelInfoFor(p.Model).DisplayName
		}
		waiting = append(waiting, PendingRequest{
			ID:    p.ID,
			Model: modelName,
			Start: p.Time,
		})
	}
	sort.Slice(waiting, func(i, j int) bool {
		if waiting[i].Start != waiting[j].Start {
			return waiting[i].Start < waiting[j].Start
		}
		return waiting[i].ID < waiting[j].ID
	})
	return SpeedLog{
		Requests:    reqs,
		Pending:     waiting,
		LastUntimed: idx.LastUntimed,
	}
}

type persistedIndex struct {
	Version     int
	Models      []string
	Records     []UsageRecord
	Cursors     map[string]FileCursor
	Timings     map[uint64]RequestTiming
	LastUntimed float64
}

func LoadUsageIndex(path string) *UsageIndex {
	f, err := os.Open(path)
	if err != nil {
		return NewUsageIndex()
	}
	defer f.Close()
	var p persistedIndex
	if err := gob.NewDecoder(f).Decode(&p); err != nil || p.Version != 1 {
		return NewUsageIndex()
	}
	idx := &UsageIndex{
		Models:      p.Models,
		Records:     p.Records,
		Cursors:     p.Cursors,
		Timings:     p.Timings,
		Starts:      map[string]RequestStart{},
		Pending:     map[string]RequestStart{},
		LastUntimed: p.LastUntimed,
	}
	idx.rebuildMaps()
	return idx
}

func (idx *UsageIndex) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	p := persistedIndex{
		Version:     1,
		Models:      idx.Models,
		Records:     idx.Records,
		Cursors:     idx.Cursors,
		Timings:     idx.Timings,
		LastUntimed: idx.LastUntimed,
	}
	if err := gob.NewEncoder(f).Encode(&p); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	idx.Dirty = false
	return os.Rename(tmp, path)
}
