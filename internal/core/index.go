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

type UsageIndex struct {
	Models       []string
	modelToIdx   map[string]uint16
	Records      []UsageRecord
	keyToIdx     map[uint64]int
	Cursors      map[string]FileCursor
	Timings      map[uint64]RequestTiming
	Pending      map[string]RequestStart
	LastUntimed  float64
	Dirty        bool
}

func NewUsageIndex() *UsageIndex {
	return &UsageIndex{
		modelToIdx: map[string]uint16{},
		keyToIdx:   map[uint64]int{},
		Cursors:    map[string]FileCursor{},
		Timings:    map[uint64]RequestTiming{},
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
	if idx.Pending == nil {
		idx.Pending = map[string]RequestStart{}
	}
}

func (idx *UsageIndex) ingestEvents(events []ParsedEvent, filePending map[string]RequestStart) bool {
	changed := false
	for _, ev := range events {
		switch ev.Kind {
		case EventRequestStarted:
			idx.Pending[ev.Start.ID] = ev.Start
			filePending[ev.Start.ID] = ev.Start
			changed = true
		case EventRequestEnded:
			// Clear oldest pending in this file if interrupted/synthetic
			for id := range filePending {
				delete(idx.Pending, id)
				delete(filePending, id)
			}
			changed = true
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
				// Streaming messages overwrite earlier partial rows with final output_tokens.
				idx.Records[pos] = rec
			} else {
				idx.keyToIdx[u.Key] = len(idx.Records)
				idx.Records = append(idx.Records, rec)
			}
			if u.RequestRef != "" {
				if start, ok := idx.Pending[u.RequestRef]; ok {
					delete(idx.Pending, u.RequestRef)
					delete(filePending, u.RequestRef)
					if u.Time > start.Time && u.Tokens.Output > 0 {
						idx.Timings[u.Key] = RequestTiming{
							Key:         u.Key,
							Start:       start.Time,
							End:         u.Time,
							Output:      u.Tokens.Output,
							ModelIdx:    mi,
							IsFast:      u.IsFast,
							IsSidechain: u.IsSidechain,
						}
					}
				} else if existing, ok := idx.Timings[u.Key]; ok {
					if u.Time >= existing.End {
						existing.End = u.Time
						existing.Output = u.Tokens.Output
						idx.Timings[u.Key] = existing
					}
				}
			} else if u.Time > idx.LastUntimed {
				idx.LastUntimed = u.Time
			}
			changed = true
		}
	}
	return changed
}

// ScanRoots incrementally scans all *.jsonl files under roots.
// onProgress is called with progress 0..1 during initial/multi-file scans.
func (idx *UsageIndex) ScanRoots(roots []string, onProgress func(float64)) (filesScanned int, changed bool) {
	var files []struct {
		path  string
		size  int64
		mtime int64
	}
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
			info, err := d.Info()
			if err != nil {
				return nil
			}
			files = append(files, struct {
				path  string
				size  int64
				mtime int64
			}{path: p, size: info.Size(), mtime: info.ModTime().UnixNano()})
			return nil
		})
	}

	var toScan []int
	for i, f := range files {
		cur, ok := idx.Cursors[f.path]
		if ok && cur.Size == f.size && cur.Mtime == f.mtime && cur.Offset <= f.size {
			continue
		}
		toScan = append(toScan, i)
	}

	total := len(toScan)
	for step, fi := range toScan {
		f := files[fi]
		cur := idx.Cursors[f.path]
		offset := cur.Offset
		if f.size < offset {
			// File truncated or replaced; re-read from start.
			offset = 0
		}
		res := ParseJSONLFile(f.path, offset)
		filePending := map[string]RequestStart{}
		if idx.ingestEvents(res.Events, filePending) {
			changed = true
		}
		idx.Cursors[f.path] = FileCursor{
			Offset: res.EndOffset,
			Size:   f.size,
			Mtime:  f.mtime,
		}
		filesScanned++
		if onProgress != nil && total > 0 && (step%8 == 0 || step == total-1) {
			onProgress(float64(step+1) / float64(total))
		}
	}

	// Prune stale timings/pending older than 2 hours
	cutoff := float64(time.Now().Add(-2 * time.Hour).Unix())
	for k, t := range idx.Timings {
		if t.End < cutoff {
			delete(idx.Timings, k)
		}
	}
	for k, p := range idx.Pending {
		if p.Time < cutoff {
			delete(idx.Pending, k)
		}
	}

	if changed {
		idx.Dirty = true
	}
	return filesScanned, changed
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
