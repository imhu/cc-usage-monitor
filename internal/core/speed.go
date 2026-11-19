package core

import (
	"math"
	"sort"
)

const (
	SpeedRecentWindow = 300.0  // 5 min
	SpeedSpanWindow   = 3600.0 // 1 hour
	SpeedPendingLimit = 600.0  // 10 min
)

type TimedRequest struct {
	ID          uint64
	Model       string
	IsFast      bool
	Start       float64
	End         float64
	Output      int
	IsSidechain bool
}

func (r TimedRequest) Duration() float64 {
	return r.End - r.Start
}

func (r TimedRequest) Rate() float64 {
	d := r.Duration()
	if d <= 0 {
		return 0
	}
	return float64(r.Output) / d
}

type PendingRequest struct {
	ID    string
	Model string
	Start float64
}

type ModelSpeed struct {
	Model    string
	IsFast   bool
	Requests []TimedRequest
	Pending  []PendingRequest
}

func (m ModelSpeed) ID() string {
	if m.IsFast {
		return m.Model + "|fast"
	}
	return m.Model
}

func (m ModelSpeed) Title(zh bool) string {
	if m.IsFast {
		if zh {
			return m.Model + " 快速"
		}
		return m.Model + " Fast"
	}
	return m.Model
}

type SpeedLog struct {
	Requests    []TimedRequest
	Pending     []PendingRequest
	LastUntimed float64
}

func (l SpeedLog) Models(now float64) []ModelSpeed {
	groups := map[string]*ModelSpeed{}
	from := now - SpeedSpanWindow - SpeedRecentWindow
	startIdx := firstRequestAfter(l.Requests, from)
	for _, r := range l.Requests[startIdx:] {
		if r.End > now {
			break
		}
		id := r.Model
		if r.IsFast {
			id += "|fast"
		}
		g := groups[id]
		if g == nil {
			g = &ModelSpeed{Model: r.Model, IsFast: r.IsFast}
			groups[id] = g
		}
		g.Requests = append(g.Requests, r)
	}
	for _, p := range l.Pending {
		if p.Start > now || now-p.Start >= SpeedPendingLimit || p.Model == "" {
			continue
		}
		g := groups[p.Model]
		if g == nil {
			g = &ModelSpeed{Model: p.Model}
			groups[p.Model] = g
		}
		g.Pending = append(g.Pending, p)
	}

	type ranked struct {
		m   ModelSpeed
		out int
	}
	list := make([]ranked, 0, len(groups))
	for _, g := range groups {
		sum := 0
		for _, r := range g.Requests {
			sum += r.Output
		}
		list = append(list, ranked{m: *g, out: sum})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].out != list[j].out {
			return list[i].out > list[j].out
		}
		return list[i].m.ID() < list[j].m.ID()
	})
	out := make([]ModelSpeed, len(list))
	for i, item := range list {
		out[i] = item.m
	}
	return out
}

type SpeedRate struct {
	Output   int
	Duration float64
	Count    int
}

func (r SpeedRate) TokensPerSecond() float64 {
	if r.Duration <= 0 {
		return 0
	}
	return float64(r.Output) / r.Duration
}

func ComputeSpeedRate(requests []TimedRequest, from, to float64) *SpeedRate {
	out := 0
	dur := 0.0
	cnt := 0
	i := firstRequestAfter(requests, from)
	for i < len(requests) && requests[i].End <= to {
		out += requests[i].Output
		dur += requests[i].Duration()
		cnt++
		i++
	}
	if cnt == 0 || dur <= 0 {
		return nil
	}
	return &SpeedRate{Output: out, Duration: dur, Count: cnt}
}

type TrendPoint struct {
	T    float64
	Rate float64
}

func ComputeSpeedTrend(requests []TimedRequest, from, to float64) []TrendPoint {
	if to <= from {
		return nil
	}
	var pts []TrendPoint
	add := func(t float64) {
		if r := ComputeSpeedRate(requests, t-SpeedRecentWindow, t); r != nil {
			pts = append(pts, TrendPoint{T: t, Rate: r.TokensPerSecond()})
		}
	}
	add(from)
	last := from
	i := firstRequestAfter(requests, from)
	for i < len(requests) && requests[i].End < to {
		t := requests[i].End
		if t > last {
			add(t)
			last = t
		}
		i++
	}
	add(to)
	return pts
}

func SpeedCeiling(requests []TimedRequest) float64 {
	if len(requests) == 0 {
		return 150
	}
	rates := make([]float64, len(requests))
	for i, r := range requests {
		rates[i] = r.Rate()
	}
	sort.Float64s(rates)
	high := rates[int(float64(len(rates)-1)*0.95)]
	c := math.Ceil(high*1.15/50.0) * 50.0
	if c < 150 {
		return 150
	}
	return c
}

func SpeedGridline(ceiling float64) float64 {
	candidates := []float64{50, 100, 200, 300, 500, 1000, 2000}
	res := 50.0
	for _, v := range candidates {
		if v <= ceiling*0.75 {
			res = v
		}
	}
	return res
}

func firstRequestAfter(requests []TimedRequest, t float64) int {
	return sort.Search(len(requests), func(i int) bool { return requests[i].End > t })
}
