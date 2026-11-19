package core

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"strings"
)

type ParsedUsage struct {
	Key         uint64
	Time        float64
	Model       string
	SessionID   string
	CWD         string
	GitBranch   string
	IsSidechain bool
	Tokens      TokenCounts
	WebSearches int
	IsFast      bool
	RequestRef  string
}

type RequestStart struct {
	ID    string
	Time  float64
	Model string
}

type EventKind uint8

const (
	EventUsage EventKind = iota + 1
	EventTitle
	EventRequestStarted
	EventRequestEnded
)

type ParsedEvent struct {
	Kind      EventKind
	Usage     ParsedUsage
	SessionID string
	Title     string
	Start     RequestStart
}

type FileParseResult struct {
	Events      []ParsedEvent
	EndOffset   int64
	BytesRead   int64
	LinesParsed int
}

var (
	needleUsage       = []byte(`"usage"`)
	needleAssistant   = []byte(`"type":"assistant"`)
	needleTitle       = []byte(`"type":"custom-title"`)
	needleRequest     = []byte(`"type":"api-request"`)
	needleUser        = []byte(`"type":"user"`)
	needleInterrupted = []byte(`"text":"[Request interrupted by user`)
)

func ParseJSONLFile(path string, offset int64) FileParseResult {
	f, err := os.Open(path)
	if err != nil {
		return FileParseResult{EndOffset: offset}
	}
	defer f.Close()
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return FileParseResult{EndOffset: offset}
		}
	}
	data, err := io.ReadAll(f)
	if err != nil || len(data) == 0 {
		return FileParseResult{EndOffset: offset}
	}
	res := ParseJSONLBytes(data)
	res.EndOffset += offset
	return res
}

func ParseJSONLBytes(data []byte) FileParseResult {
	var events []ParsedEvent
	lines := 0
	pos := 0
	for pos < len(data) {
		nl := bytes.IndexByte(data[pos:], '\n')
		if nl < 0 {
			// Incomplete line still being written; leave for next incremental read.
			break
		}
		line := data[pos : pos+nl]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if len(line) > 0 {
			if ev, ok := parseJSONLLine(line); ok {
				events = append(events, ev)
			}
			lines++
		}
		pos += nl + 1
	}
	return FileParseResult{
		Events:      events,
		EndOffset:   int64(pos),
		BytesRead:   int64(pos),
		LinesParsed: lines,
	}
}

type titleRow struct {
	Type        string `json:"type"`
	CustomTitle string `json:"customTitle"`
	SessionID   string `json:"sessionId"`
}

type requestRow struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Params    *struct {
		Model string `json:"model"`
	} `json:"params"`
}

type assistantRow struct {
	Type        string `json:"type"`
	Timestamp   string `json:"timestamp"`
	RequestID   string `json:"requestId"`
	RequestRef  string `json:"requestRef"`
	SessionID   string `json:"sessionId"`
	CWD         string `json:"cwd"`
	GitBranch   string `json:"gitBranch"`
	IsSidechain bool   `json:"isSidechain"`
	Message     *struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreation            *struct {
				Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
				Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
			ServerToolUse *struct {
				WebSearchRequests int64 `json:"web_search_requests"`
			} `json:"server_tool_use"`
			Speed string `json:"speed"`
		} `json:"usage"`
	} `json:"message"`
}

func parseJSONLLine(line []byte) (ParsedEvent, bool) {
	if len(line) < 8192 && bytes.Contains(line, needleTitle) {
		var r titleRow
		if json.Unmarshal(line, &r) == nil && r.Type == "custom-title" && r.SessionID != "" && strings.TrimSpace(r.CustomTitle) != "" {
			return ParsedEvent{Kind: EventTitle, SessionID: r.SessionID, Title: r.CustomTitle}, true
		}
		return ParsedEvent{}, false
	}
	if bytes.Contains(line, needleRequest) {
		var r requestRow
		if json.Unmarshal(line, &r) == nil && r.Type == "api-request" && r.ID != "" {
			if ts, ok := ParseTimestamp(r.Timestamp); ok {
				model := ""
				if r.Params != nil {
					model = r.Params.Model
				}
				return ParsedEvent{
					Kind:  EventRequestStarted,
					Start: RequestStart{ID: r.ID, Time: ts, Model: model},
				}, true
			}
		}
		return ParsedEvent{}, false
	}
	if len(line) < 4096 && bytes.Contains(line, needleInterrupted) && bytes.Contains(line, needleUser) {
		return ParsedEvent{Kind: EventRequestEnded}, true
	}
	if !bytes.Contains(line, needleUsage) || !bytes.Contains(line, needleAssistant) {
		return ParsedEvent{}, false
	}
	var r assistantRow
	if json.Unmarshal(line, &r) != nil || r.Type != "assistant" || r.Message == nil || r.Message.Model == "" {
		return ParsedEvent{}, false
	}
	if r.Message.Model == "<synthetic>" {
		return ParsedEvent{Kind: EventRequestEnded}, true
	}
	if r.Message.Usage == nil {
		return ParsedEvent{}, false
	}
	ts, ok := ParseTimestamp(r.Timestamp)
	if !ok {
		return ParsedEvent{}, false
	}
	u := r.Message.Usage
	written := u.CacheCreationInputTokens
	var write5m, write1h int64
	if u.CacheCreation != nil {
		write5m = u.CacheCreation.Ephemeral5m
		write1h = u.CacheCreation.Ephemeral1h
	}
	if write5m+write1h < written {
		write5m = written - write1h
	}
	tokens := TokenCounts{
		Input:        u.InputTokens,
		Output:       u.OutputTokens,
		CacheWrite5m: write5m,
		CacheWrite1h: write1h,
		CacheRead:    u.CacheReadInputTokens,
	}
	var key uint64
	if r.Message.ID != "" || r.RequestID != "" {
		key = fnv1aSeed(r.RequestID, fnv1a(r.Message.ID+"|"))
	} else {
		key = fnv1a(r.Timestamp + "|" + r.Message.Model + "|" +
			strconv.FormatInt(tokens.Input, 10) + "|" +
			strconv.FormatInt(tokens.Output, 10) + "|" +
			strconv.FormatInt(tokens.CacheRead, 10))
	}
	webSearches := 0
	if u.ServerToolUse != nil {
		webSearches = int(u.ServerToolUse.WebSearchRequests)
	}
	sid := r.SessionID
	if sid == "" {
		sid = "unknown"
	}
	return ParsedEvent{
		Kind: EventUsage,
		Usage: ParsedUsage{
			Key:         key,
			Time:        ts,
			Model:       r.Message.Model,
			SessionID:   sid,
			CWD:         r.CWD,
			GitBranch:   r.GitBranch,
			IsSidechain: r.IsSidechain,
			Tokens:      tokens,
			WebSearches: webSearches,
			IsFast:      u.Speed == "fast",
			RequestRef:  r.RequestRef,
		},
	}, true
}

// ParseTimestamp fast-parses ISO 8601 strings (e.g. 2026-09-28T14:40:53.806Z / +08:00) into Unix seconds.
func ParseTimestamp(s string) (float64, bool) {
	if len(s) < 19 {
		return 0, false
	}
	num := func(i, n int) (int, bool) {
		if i+n > len(s) {
			return 0, false
		}
		v := 0
		for k := 0; k < n; k++ {
			c := s[i+k]
			if c < '0' || c > '9' {
				return 0, false
			}
			v = v*10 + int(c-'0')
		}
		return v, true
	}
	year, ok1 := num(0, 4)
	month, ok2 := num(5, 2)
	day, ok3 := num(8, 2)
	hour, ok4 := num(11, 2)
	minute, ok5 := num(14, 2)
	second, ok6 := num(17, 2)
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || !ok6 ||
		s[4] != '-' || s[7] != '-' || (s[10] != 'T' && s[10] != ' ') || s[13] != ':' || s[16] != ':' {
		return 0, false
	}
	i := 19
	fraction := 0.0
	if i < len(s) && s[i] == '.' {
		i++
		scale := 0.1
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			fraction += float64(s[i]-'0') * scale
			scale *= 0.1
			i++
		}
	}
	offset := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		sign := 1
		if s[i] == '-' {
			sign = -1
		}
		oh, _ := num(i+1, 2)
		var om int
		if i+3 < len(s) && s[i+3] == ':' {
			om, _ = num(i+4, 2)
		} else {
			om, _ = num(i+3, 2)
		}
		offset = sign * (oh*3600 + om*60)
	}
	days := daysFromCivil(year, month, day)
	return float64(days*86400+hour*3600+minute*60+second-offset) + fraction, true
}

func daysFromCivil(y0, m, d int) int {
	y := y0
	if m <= 2 {
		y--
	}
	var era int
	if y >= 0 {
		era = y / 400
	} else {
		era = (y - 399) / 400
	}
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

func fnv1a(s string) uint64 {
	return fnv1aSeed(s, 0xcbf29ce484222325)
}

func fnv1aSeed(s string, seed uint64) uint64 {
	h := seed
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 0x00000100000001B3
	}
	return h
}
