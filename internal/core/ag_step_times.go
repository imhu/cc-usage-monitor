package core

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

var errInvalidStepProto = errors.New("ag: unreadable step metadata protobuf")

// AGStepTiming holds exact millisecond timing parsed from Antigravity's
// CortexStepMetadata protobuf inside conversations/<id>.db.
type AGStepTiming struct {
	StepIndex  int
	Start      time.Time
	First      time.Time // viewable_at (first content)
	End        time.Time // finished_generating_at
	TTFTMs     int64
	DurationMs int64
	DecodeMs   int64
	ResponseID string
}

// ReadAGConversationStepTimings opens dbPath strictly read-only and extracts
// per-step millisecond timings keyed by step index.
func ReadAGConversationStepTimings(dbPath string) (map[int]AGStepTiming, error) {
	if dbPath == "" {
		return nil, os.ErrNotExist
	}
	if _, err := os.Stat(dbPath); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", sqliteReadOnlyURI(dbPath))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.Query("SELECT idx, metadata FROM steps WHERE metadata IS NOT NULL ORDER BY idx")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	timings := map[int]AGStepTiming{}
	byResp := map[string]AGStepTiming{}

	for rows.Next() {
		var idx int
		var meta []byte
		if err := rows.Scan(&idx, &meta); err != nil {
			continue
		}
		t, ok := parseAGStepTiming(idx, meta)
		if !ok {
			continue
		}
		if t.ResponseID != "" {
			if prev, seen := byResp[t.ResponseID]; seen {
				if !prev.Start.IsZero() && (t.Start.IsZero() || prev.Start.Before(t.Start)) {
					t.Start = prev.Start
				}
				if !prev.First.IsZero() && (t.First.IsZero() || prev.First.Before(t.First)) {
					t.First = prev.First
				}
			}
			byResp[t.ResponseID] = t
		}
		computeStepTimingFields(&t)
		timings[idx] = t
	}
	if err := rows.Err(); err != nil {
		return timings, err
	}
	return timings, nil
}

func sqliteReadOnlyURI(dbPath string) string {
	slashed := filepath.ToSlash(filepath.Clean(dbPath))
	u := url.URL{
		Scheme:   "file",
		Path:     slashed,
		RawQuery: "mode=ro&_pragma=busy_timeout(2000)&_pragma=query_only(ON)",
	}
	if len(u.Path) >= 2 && u.Path[1] == ':' {
		u.Path = "/" + u.Path
	}
	return u.String()
}

func parseAGStepTiming(idx int, metadata []byte) (AGStepTiming, bool) {
	t := AGStepTiming{StepIndex: idx}
	var usage []byte
	if err := pbWalk(metadata, func(num int, _ uint64, d []byte) {
		if d == nil {
			return
		}
		switch num {
		case 1:
			t.Start = pbTimestamp(d)
		case 6:
			t.First = pbTimestamp(d)
		case 7:
			t.End = pbTimestamp(d)
		case 9:
			usage = d
		}
	}); err != nil {
		return AGStepTiming{}, false
	}
	if len(usage) > 0 {
		_ = pbWalk(usage, func(num int, _ uint64, d []byte) {
			if num == 11 && d != nil {
				t.ResponseID = string(d)
			}
		})
	}
	if t.Start.IsZero() && t.End.IsZero() {
		return AGStepTiming{}, false
	}
	return t, true
}

func computeStepTimingFields(t *AGStepTiming) {
	t.TTFTMs = 0
	t.DurationMs = 0
	t.DecodeMs = 0
	if t.Start.IsZero() || t.End.IsZero() || t.End.Before(t.Start) {
		return
	}
	t.DurationMs = max(t.End.Sub(t.Start).Milliseconds(), 1)
	if !t.First.IsZero() && !t.First.Before(t.Start) && !t.First.After(t.End) {
		t.TTFTMs = max(t.First.Sub(t.Start).Milliseconds(), 1)
		t.DecodeMs = t.End.Sub(t.First).Milliseconds()
	}
}

func pbTimestamp(b []byte) time.Time {
	var sec, nanos uint64
	if err := pbWalk(b, func(num int, v uint64, d []byte) {
		if d != nil {
			return
		}
		switch num {
		case 1:
			sec = v
		case 2:
			nanos = v
		}
	}); err != nil {
		return time.Time{}
	}
	if sec == 0 || sec > 1<<40 || nanos >= 1e9 {
		return time.Time{}
	}
	return time.Unix(int64(sec), int64(nanos)).UTC()
}

func pbWalk(b []byte, field func(num int, v uint64, d []byte)) error {
	for len(b) > 0 {
		tag, n := binary.Uvarint(b)
		if n <= 0 || tag>>3 == 0 {
			return errInvalidStepProto
		}
		b = b[n:]
		num := int(tag >> 3)
		switch tag & 7 {
		case 0:
			v, n := binary.Uvarint(b)
			if n <= 0 {
				return errInvalidStepProto
			}
			b = b[n:]
			field(num, v, nil)
		case 1:
			if len(b) < 8 {
				return errInvalidStepProto
			}
			b = b[8:]
		case 2:
			l, n := binary.Uvarint(b)
			if n <= 0 || l > uint64(len(b)-n) {
				return errInvalidStepProto
			}
			field(num, 0, b[n:n+int(l)])
			b = b[n+int(l):]
		case 5:
			if len(b) < 4 {
				return errInvalidStepProto
			}
			b = b[4:]
		default:
			return errInvalidStepProto
		}
	}
	return nil
}
