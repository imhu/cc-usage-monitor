package core

// TokenCounts holds the token breakdown for one or more assistant messages.
type TokenCounts struct {
	Input        int64
	Output       int64
	CacheWrite5m int64
	CacheWrite1h int64
	CacheRead    int64
}

func (t TokenCounts) CacheWrite() int64 {
	return t.CacheWrite5m + t.CacheWrite1h
}

func (t TokenCounts) Prompt() int64 {
	return t.Input + t.CacheWrite5m + t.CacheWrite1h + t.CacheRead
}

func (t TokenCounts) Total() int64 {
	return t.Prompt() + t.Output
}

// CacheHitRate is the fraction of prompt tokens served from cache.
func (t TokenCounts) CacheHitRate() float64 {
	p := t.Prompt()
	if p <= 0 {
		return 0
	}
	return float64(t.CacheRead) / float64(p)
}

func (t *TokenCounts) Add(o TokenCounts) {
	t.Input += o.Input
	t.Output += o.Output
	t.CacheWrite5m += o.CacheWrite5m
	t.CacheWrite1h += o.CacheWrite1h
	t.CacheRead += o.CacheRead
}
