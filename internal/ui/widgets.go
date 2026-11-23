package appui

import (
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"ccmonitor/internal/core"
)

func Hairline(c *ui.Context) {
	ui.Box(c).FillWidth().Height(1).Background(ColorDivider)
}

func PlanTag(c *ui.Context, plan core.PlanKind) {
	title := strings.ToUpper(plan.Title())
	if title == "" {
		return
	}
	var fill, stroke, text ui.Color
	switch plan {
	case core.PlanPro:
		fill = ui.Transparent
		stroke = ColorAccent.Alpha(0.6)
		text = ui.Hex("#E0896C")
	case core.PlanMax5x, core.PlanMax20x:
		fill = ColorAccent.Alpha(0.20)
		stroke = ui.Transparent
		text = ui.Hex("#E8977C")
	case core.PlanUltra:
		fill = ColorAccent
		stroke = ui.Transparent
		text = ui.Hex("#3A1A0E")
	default:
		fill = ui.RGBA(255, 255, 255, 0.08)
		stroke = ui.Transparent
		text = ColorSecondary
	}
	box := ui.Row(c).Height(16).Padding(0, 6).Radius(8).Center().Background(fill)
	if stroke.A > 0 {
		box.Border(1, stroke)
	}
	box.Children(func() {
		ui.Text(c, title).FontSize(9.5).FontWeight(600).LetterSpacing(0.8).TextColor(text).SingleLine()
	})
}

func Chip(c *ui.Context, title string, col ui.Color) ui.Element {
	return ui.Row(c).Height(16).Padding(0, 6).Radius(8).Center().Background(col.Alpha(0.16)).Children(func() {
		ui.Text(c, title).FontSize(10).FontWeight(600).TextColor(col).SingleLine()
	})
}

func SectionHeader(c *ui.Context, key, title string, collapsed bool, onToggle func(), trailing func()) {
	row := ui.Row(c.Key(key)).FillWidth().AlignItems(ui.Center).Gap(6).Cursor(ui.CursorPointer)
	hovered := row.Hovered()
	if row.Clicked() && onToggle != nil {
		onToggle()
	}
	row.Children(func() {
		titleColor := ColorSecondary
		if hovered {
			titleColor = ColorText
		}
		ui.Text(c, title).FontSize(11.5).FontWeight(500).TextColor(titleColor).SingleLine()
		if collapsed || hovered {
			ic := IconChevDn
			if collapsed {
				ic = IconChevRt
			}
			ui.Icon(c, ic).FontSize(9).TextColor(ColorTertiary)
		}
		ui.Spacer(c)
		if trailing != nil {
			trailing()
		}
	})
}

// ThinBar replicates Gauges.swift ThinBar 1:1:
// 4px rounded track, colored usage capsule, optional white vertical pace mark
// (with red exceeded segment past the mark and hover safe-zone highlight).
func ThinBar(c *ui.Context, key string, fraction float64, fillColor ui.Color, paceMark *float64, exceeded bool, hoveredOut *bool) {
	frac := float32(math.Max(0, math.Min(1, fraction)))
	bar := ui.Box(c.Key(key)).FillWidth().Height(10).Center()
	pointed := bar.Hovered() && paceMark != nil && hoveredOut != nil
	if hoveredOut != nil {
		*hoveredOut = pointed
	}
	shownFrac := bar.Animate("frac", frac, 350*time.Millisecond)

	bar.Draw(func(p *ui.Painter, r ui.Rect) {
		const barH float32 = 4
		y := r.Y + (r.H-barH)/2
		w := r.W
		// Base track
		p.Fill(ui.Rect{X: r.X, Y: y, W: w, H: barH}, ColorTrack, barH/2)

		var markX float32
		if paceMark != nil {
			m := float32(math.Max(0, math.Min(1, *paceMark)))
			markX = w * m
			if pointed && markX > 0 {
				sw := max(barH, markX)
				p.Fill(ui.Rect{X: r.X, Y: y, W: sw, H: barH}, ui.RGBA(255, 255, 255, 0.09), barH/2)
			}
		}

		if shownFrac > 0.001 {
			fw := max(barH, w*shownFrac)
			p.Fill(ui.Rect{X: r.X, Y: y, W: fw, H: barH}, fillColor, barH/2)
			if exceeded && paceMark != nil && fw > markX {
				p.Clip(ui.Rect{X: r.X + markX, Y: y, W: fw - markX, H: barH}, 0, func() {
					p.Fill(ui.Rect{X: r.X, Y: y, W: fw, H: barH}, ColorCritical, barH/2)
				})
			}
		}

		if paceMark != nil {
			mh := barH + 4
			if pointed {
				mh = barH + 6
			}
			my := r.Y + (r.H-mh)/2
			p.Fill(ui.Rect{X: r.X + markX - 0.75, Y: my, W: 1.5, H: mh}, ColorText, 0.75)
		}
	})
}

// MiniBars replicates Gauges.swift MiniBars 1:1 (104x30 14-day bar chart).
func MiniBars(c *ui.Context, key string, values []float64, highlightIdx int, periodStart int, hoveredIdx *int) {
	MiniBarsColored(c, key, values, highlightIdx, periodStart, ColorAccent, hoveredIdx)
}

func MiniBarsColored(c *ui.Context, key string, values []float64, highlightIdx int, periodStart int, accent ui.Color, hoveredIdx *int) {
	box := ui.Box(c.Key(key)).Size(104, 30)
	n := max(1, len(values))
	if box.Hovered() {
		px, _, ok := box.PointerPosition()
		if ok {
			const spacing = 2.0
			bw := max(1.0, (104.0-spacing*float64(n-1))/float64(n))
			idx := int(float64(px) / (bw + spacing))
			if idx < 0 {
				idx = 0
			}
			if idx >= len(values) {
				idx = len(values) - 1
			}
			*hoveredIdx = idx
		}
	} else {
		*hoveredIdx = -1
	}

	curHover := *hoveredIdx
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		if len(values) == 0 {
			return
		}
		const spacing float32 = 2
		bw := max(1, (r.W-spacing*float32(len(values)-1))/float32(len(values)))
		maxVal := 0.0001
		for _, v := range values {
			if v > maxVal {
				maxVal = v
			}
		}
		for i, v := range values {
			h := max(1.5, r.H*float32(v/maxVal))
			col := ui.RGBA(255, 255, 255, 0.14)
			if periodStart < 0 || i >= periodStart {
				col = ui.RGBA(255, 255, 255, 0.34)
			}
			if curHover == i || (curHover < 0 && i == highlightIdx) {
				col = accent
			}
			x := r.X + float32(i)*(bw+spacing)
			y := r.Y + r.H - h
			rad := min(1.5, bw/2)
			p.Fill(ui.Rect{X: x, Y: y, W: bw, H: h}, col, rad)
		}
	})
}

// SpeedSparkline replicates SpeedSection.swift SpeedSparkline 1:1 (104x30 smooth curve + accent dot).
func SpeedSparkline(c *ui.Context, key string, trend []core.TrendPoint, nowSec float64, hoveredAge *float64) {
	box := ui.Box(c.Key(key)).Size(104, 30).Margin(0, 0, 3, 0)
	prevAge := *hoveredAge
	nextAge := -1.0
	if box.Hovered() && len(trend) > 0 {
		if px, _, ok := box.PointerPosition(); ok {
			frac := max(0.0, min(1.0, float64(px)/104.0))
			t := nowSec - core.SpeedSpanWindow*(1.0-frac)
			if ni := nearestTrendIndex(trend, t); ni >= 0 {
				nextAge = nowSec - trend[ni].T
			}
		}
	}
	if nextAge != prevAge {
		*hoveredAge = nextAge
		c.Invalidate()
	}

	curAge := *hoveredAge
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		if len(trend) == 0 {
			p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H - 1.5, W: r.W, H: 1.5}, ui.RGBA(255, 255, 255, 0.12), 0.75)
			return
		}
		low, high := trend[0].Rate, trend[0].Rate
		for _, pt := range trend {
			if pt.Rate < low {
				low = pt.Rate
			}
			if pt.Rate > high {
				high = pt.Rate
			}
		}
		span := max(high-low, high*0.3, 1.0)
		bottom := max(0.0, (low+high)/2.0-span*0.6)
		rng := span * 1.2

		pts := make([]ui.Point, len(trend))
		for i, tp := range trend {
			x := r.X + r.W*float32(1.0-(nowSec-tp.T)/core.SpeedSpanWindow)
			rise := (r.H - 2) * float32(max(0.0, min(1.0, (tp.Rate-bottom)/rng)))
			pts[i] = ui.Point{X: x, Y: r.Y + r.H - 1 - rise}
		}

		if len(pts) == 1 {
			p.Fill(ui.Rect{X: pts[0].X - 0.75, Y: pts[0].Y - 0.75, W: 1.5, H: 1.5}, ui.RGBA(255, 255, 255, 0.55), 0.75)
		} else {
			path := buildMonotonePath(pts)
			p.StrokePath(path, 1.5, ui.RGBA(255, 255, 255, 0.55))
		}

		// Live orange dot at right end only when last point is now (recent 5m has replies).
		last := trend[len(trend)-1]
		if last.T == nowSec {
			lp := pts[len(pts)-1]
			p.Fill(ui.Rect{X: lp.X - 2.5, Y: lp.Y - 2.5, W: 5, H: 5}, ColorAccent, 2.5)
		}
		if curAge >= 0 {
			if ni := nearestTrendIndex(trend, nowSec-curAge); ni >= 0 {
				hp := pts[ni]
				p.Fill(ui.Rect{X: hp.X - 2, Y: hp.Y - 2, W: 4, H: 4}, ColorText, 2)
			}
		}
	})
}

// SpeedChart replicates SpeedSection.swift detailed scatter + trend + pending bars (64px tall).
func SpeedChart(c *ui.Context, key string, requests []core.TimedRequest, pending []core.PendingRequest, nowSec float64, hoveredID *uint64) {
	box := ui.Box(c.Key(key)).FillWidth().Height(64)
	var shown []core.TimedRequest
	for _, r := range requests {
		if r.End > nowSec-core.SpeedSpanWindow && r.End <= nowSec {
			shown = append(shown, r)
		}
	}
	ceiling := core.SpeedCeiling(shown)
	const inset float32 = 4

	prevID := *hoveredID
	var nextID uint64
	if box.Hovered() && len(shown) > 0 {
		if px, py, ok := box.PointerPosition(); ok {
			w := PopoverWidth - PanelPadH*2
			plotW := max(1, w-inset*2)
			bestDist := float32(144)
			for _, req := range shown {
				qx := inset + plotW*float32(1.0-(nowSec-req.End)/core.SpeedSpanWindow)
				qy := (64 - 5) - (64-10)*float32(min(req.Rate(), ceiling)/ceiling)
				d := (qx-px)*(qx-px) + (qy-py)*(qy-py)
				if d < bestDist {
					bestDist = d
					nextID = req.ID
				}
			}
		}
	}
	if nextID != prevID {
		*hoveredID = nextID
		c.Invalidate()
	}

	curHover := *hoveredID
	trend := core.ComputeSpeedTrend(requests, nowSec-core.SpeedSpanWindow, nowSec)
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		plotW := max(1, r.W-inset*2)
		xOf := func(t float64) float32 {
			return r.X + inset + plotW*float32(1.0-(nowSec-t)/core.SpeedSpanWindow)
		}
		yOf := func(rate float64) float32 {
			top := r.Y + 5
			bot := r.Y + r.H - 5
			return bot - (bot-top)*float32(min(rate, ceiling)/ceiling)
		}

		// Reference gridline
		grid := core.SpeedGridline(ceiling)
		gy := yOf(grid)
		p.Fill(ui.Rect{X: r.X, Y: gy - 0.25, W: r.W, H: 0.5}, ColorDivider, 0)
		label := ui.Span{Text: core.FormatGrouped(int64(grid)), Size: 9.5, Weight: 500, Color: ui.RGBA(255, 255, 255, 0.28)}
		_, lh := p.MeasureText(0, label)
		p.RichText(r.X, gy-lh-1, 0, label)

		// Scatter dots
		for _, req := range shown {
			cx, cy := xOf(req.End), yOf(req.Rate())
			rad := float32(min(3.0, 1.1+math.Sqrt(float64(req.Output))/45.0))
			col := ui.RGBA(255, 255, 255, 0.22)
			if req.ID == curHover {
				col = ColorAccent
			}
			p.Fill(ui.Rect{X: cx - rad, Y: cy - rad, W: rad * 2, H: rad * 2}, col, rad)
		}

		// Trend smooth line
		if len(trend) > 0 {
			pts := make([]ui.Point, len(trend))
			for i, tp := range trend {
				pts[i] = ui.Point{X: xOf(tp.T), Y: yOf(tp.Rate)}
			}
			if len(pts) == 1 {
				p.Fill(ui.Rect{X: pts[0].X - 0.75, Y: pts[0].Y - 0.75, W: 1.5, H: 1.5}, ui.RGBA(255, 255, 255, 0.90), 0.75)
			} else {
				p.StrokePath(buildMonotonePath(pts), 1.5, ui.RGBA(255, 255, 255, 0.90))
			}
		}

		// Pending live green bars
		for i, pend := range pending {
			x0 := max(r.X, xOf(pend.Start))
			x1 := xOf(nowSec)
			bx := min(x0, x1-2)
			bw := max(2, x1-x0)
			by := r.Y + r.H - 1.5 - float32(i)*3
			p.Fill(ui.Rect{X: bx, Y: by, W: bw, H: 1.5}, ColorLive, 0.75)
		}
	})
}

func nearestTrendIndex(trend []core.TrendPoint, t float64) int {
	if len(trend) == 0 {
		return -1
	}
	lo, hi := 0, len(trend)
	for lo < hi {
		mid := (lo + hi) / 2
		if trend[mid].T < t {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return 0
	}
	if lo == len(trend) {
		return len(trend) - 1
	}
	if t-trend[lo-1].T <= trend[lo].T-t {
		return lo - 1
	}
	return lo
}

// buildMonotonePath implements Steffen's monotone cubic spline (matching SpeedLine.swift).
func buildMonotonePath(raw []ui.Point) *ui.Path {
	pts := make([]ui.Point, 0, len(raw))
	for _, p := range raw {
		if len(pts) > 1 && p.X-pts[len(pts)-1].X < 1.5 {
			pts[len(pts)-1] = p
		} else {
			pts = append(pts, p)
		}
	}
	path := &ui.Path{}
	if len(pts) == 0 {
		return path
	}
	path.MoveTo(pts[0].X, pts[0].Y)
	if len(pts) == 1 {
		path.LineTo(pts[0].X+0.1, pts[0].Y)
		return path
	}
	if len(pts) == 2 {
		path.LineTo(pts[1].X, pts[1].Y)
		return path
	}
	n := len(pts)
	h := make([]float32, n-1)
	slope := make([]float32, n-1)
	for i := 0; i < n-1; i++ {
		h[i] = pts[i+1].X - pts[i].X
		if h[i] > 0 {
			slope[i] = (pts[i+1].Y - pts[i].Y) / h[i]
		}
	}
	sign := func(v float32) float32 {
		if v > 0 {
			return 1
		}
		if v < 0 {
			return -1
		}
		return 0
	}
	abs32 := func(v float32) float32 {
		if v < 0 {
			return -v
		}
		return v
	}
	tan := make([]float32, n)
	for i := 1; i < n-1; i++ {
		s0, s1 := slope[i-1], slope[i]
		denom := h[i-1] + h[i]
		if denom <= 0 {
			continue
		}
		mean := (s0*h[i] + s1*h[i-1]) / denom
		lim := min(abs32(s0), min(abs32(s1), abs32(mean)/2))
		tan[i] = (sign(s0) + sign(s1)) * lim
	}
	endTan := func(s, nb float32) float32 {
		t := (3*s - nb) / 2
		if sign(t) != sign(s) {
			return 0
		}
		if abs32(t) > 3*abs32(s) {
			return 3 * s
		}
		return t
	}
	tan[0] = endTan(slope[0], tan[1])
	tan[n-1] = endTan(slope[n-2], tan[n-2])

	for i := 0; i < n-1; i++ {
		dx := h[i] / 3
		path.CubeTo(
			pts[i].X+dx, pts[i].Y+dx*tan[i],
			pts[i+1].X-dx, pts[i+1].Y-dx*tan[i+1],
			pts[i+1].X, pts[i+1].Y,
		)
	}
	return path
}

// SegmentedTabs replicates SpanTabs & Settings segmented pickers 1:1.
func SegmentedTabs(c *ui.Context, key string, labels []string, selected int, fullWidth bool, onSelect func(int)) {
	wrap := ui.Row(c.Key(key)).Padding(2).Gap(2).Radius(9).Background(ColorPillBg)
	if fullWidth {
		wrap.FillWidth()
	}
	wrap.Children(func() {
		for i, label := range labels {
			isSel := i == selected
			tab := ui.Row(c.Key(i)).Height(26).Padding(0, 12).Radius(7).Center().Cursor(ui.CursorPointer)
			if fullWidth {
				tab.Grow(1)
			}
			if isSel {
				tab.Background(ColorPillSel)
			} else if tab.Hovered() {
				tab.Background(ColorHover)
			}
			if tab.Clicked() && !isSel && onSelect != nil {
				onSelect(i)
			}
			tab.Children(func() {
				col := ColorSecondary
				weight := 400
				if isSel {
					col = ColorText
					weight = 500
				}
				ui.Text(c, label).FontSize(12).FontWeight(weight).TextColor(col).SingleLine()
			})
		}
	})
}

func FooterButton(c *ui.Context, key string, icon *ui.SVG, title string, spinning bool, onClick func()) ui.Element {
	btn := ui.Row(c.Key(key)).Height(24).Padding(0, 8).Gap(5).Radius(6).AlignItems(ui.Center).Cursor(ui.CursorPointer)
	hov := btn.Hovered()
	if hov {
		btn.Background(ColorHover)
	}
	if btn.Clicked() && onClick != nil {
		onClick()
	}
	col := ColorSecondary
	if hov {
		col = ColorText
	}
	btn.Children(func() {
		ic := ui.Icon(c, icon).FontSize(12.5).TextColor(col)
		if spinning {
			ic.Rotate(ic.Loop("spin", 750*time.Millisecond, ui.Linear) * 360)
		}
		ui.Text(c, title).FontSize(11.5).TextColor(col).SingleLine()
	})
	return btn
}

func EmptyNote(c *ui.Context, icon *ui.SVG, title, detail string, action func()) {
	ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(10).Children(func() {
		ui.Row(c).Size(28, 28).Radius(8).Center().Background(ColorHover).Children(func() {
			ui.Icon(c, icon).FontSize(14).TextColor(ColorTertiary)
		})
		ui.Column(c).Grow(1).Gap(3).Children(func() {
			ui.Text(c, title).FontSize(12).FontWeight(500).TextColor(ColorSecondary)
			if detail != "" {
				ui.Text(c, detail).FontSize(10.5).TextColor(ColorTertiary)
			}
			if action != nil {
				action()
			}
		})
	})
}

// TerracottaSwitch replicates the exact #D97757 pill switch from Settings screenshots.
func TerracottaSwitch(c *ui.Context, key string, on bool, onToggle func(bool)) {
	AccentSwitch(c, key, on, ColorAccent, onToggle)
}

func AccentSwitch(c *ui.Context, key string, on bool, accent ui.Color, onToggle func(bool)) {
	sw := ui.Box(c.Key(key)).Size(38, 22).Radius(11).Cursor(ui.CursorPointer)
	if sw.Clicked() && onToggle != nil {
		onToggle(!on)
	}
	target := float32(0)
	if on {
		target = 1
	}
	prog := sw.Animate("on", target, 160*time.Millisecond)
	sw.Draw(func(p *ui.Painter, r ui.Rect) {
		bg := ui.RGBA(255, 255, 255, 0.16)
		if prog > 0.5 {
			bg = accent
		}
		p.Fill(r, bg, r.H/2)
		knobSize := r.H - 4
		kx := r.X + 2 + (r.W-knobSize-4)*prog
		ky := r.Y + 2
		p.Fill(ui.Rect{X: kx, Y: ky, W: knobSize, H: knobSize}, ui.RGB(255, 255, 255), knobSize/2)
	})
}

func AGPlanTag(c *ui.Context, rawTier string) {
	lower := strings.ToLower(strings.TrimSpace(rawTier))
	if lower == "" {
		return
	}
	var label string
	var fill, stroke, text ui.Color
	switch {
	case strings.Contains(lower, "ultra"):
		label = "ULTRA"
		fill = ColorAGAccent
		text = ui.Hex("#0A1930")
	case strings.Contains(lower, "max"):
		label = "MAX"
		fill = ColorAGAccent.Alpha(0.22)
		text = ui.Hex("#89B4FF")
	case strings.Contains(lower, "pro"):
		label = "PRO"
		fill = ui.Transparent
		stroke = ColorAGAccent.Alpha(0.65)
		text = ui.Hex("#89B4FF")
	default:
		label = strings.ToUpper(strings.TrimSpace(rawTier))
		fill = ui.RGBA(255, 255, 255, 0.08)
		text = ColorSecondary
	}
	box := ui.Row(c).Height(16).Padding(0, 6).Radius(8).Center().Background(fill)
	if stroke.A > 0 {
		box.Border(1, stroke)
	}
	box.Children(func() {
		ui.Text(c, label).FontSize(9.5).FontWeight(600).LetterSpacing(0.8).TextColor(text).SingleLine()
	})
}

func AGSparkline(c *ui.Context, key string, samples []core.AGSpeedSample, hoveredIdx *int, onClick func()) {
	n := len(samples)
	if n > 18 {
		samples = samples[n-18:]
		n = 18
	}
	box := ui.Box(c.Key(key)).Size(104, 30).Margin(0, 0, 3, 0).Cursor(ui.CursorPointer)
	prevIdx := *hoveredIdx
	nextIdx := -1
	if box.Hovered() && n >= 2 {
		if px, _, ok := box.PointerPosition(); ok {
			ratio := max(0.0, min(1.0, float64(px)/104.0))
			nextIdx = int(math.Round(ratio * float64(n-1)))
		}
	}
	if nextIdx != prevIdx {
		*hoveredIdx = nextIdx
		c.Invalidate()
	}
	if box.Clicked() && onClick != nil {
		onClick()
	}

	curIdx := *hoveredIdx
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		if n < 2 {
			p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H - 1.5, W: r.W, H: 1.5}, ui.RGBA(255, 255, 255, 0.12), 0.75)
			return
		}
		minV, maxV := samples[0].TokensPerSec, samples[0].TokensPerSec
		for _, s := range samples {
			if s.TokensPerSec < minV {
				minV = s.TokensPerSec
			}
			if s.TokensPerSec > maxV {
				maxV = s.TokensPerSec
			}
		}
		low := minV * 0.85
		high := max(low+1.0, maxV*1.05)

		pts := make([]ui.Point, n)
		for i, s := range samples {
			x := r.X + r.W*float32(i)/float32(n-1)
			norm := float32((s.TokensPerSec - low) / (high - low))
			y := r.Y + r.H - 3 - norm*(r.H-6)
			pts[i] = ui.Point{X: x, Y: y}
		}
		lineCol := ui.RGBA(255, 255, 255, 0.45)
		if curIdx >= 0 {
			lineCol = ui.RGBA(255, 255, 255, 0.65)
		}
		p.StrokePath(buildMonotonePath(pts), 1.5, lineCol)

		active := n - 1
		if curIdx >= 0 && curIdx < n {
			active = curIdx
			p.Fill(ui.Rect{X: pts[active].X - 0.5, Y: r.Y, W: 1, H: r.H}, ui.RGBA(255, 255, 255, 0.16), 0)
		}
		dotR := float32(2.25)
		if curIdx >= 0 {
			dotR = 3.0
		}
		ap := pts[active]
		p.Fill(ui.Rect{X: ap.X - dotR, Y: ap.Y - dotR, W: dotR * 2, H: dotR * 2}, ColorAGAccent, dotR)
	})
}

func AGSpeedDotChart(c *ui.Context, key string, samples []core.AGSpeedSample, now time.Time, hoveredID *int, onClick func()) {
	box := ui.Box(c.Key(key)).FillWidth().Height(64).Radius(7).Background(ui.RGBA(255, 255, 255, 0.03)).Cursor(ui.CursorPointer)
	if len(samples) == 0 {
		return
	}
	minSpeed := samples[0].TokensPerSec
	maxSpeed := samples[0].TokensPerSec
	var maxOut int64 = 1
	for _, s := range samples {
		if s.TokensPerSec < minSpeed {
			minSpeed = s.TokensPerSec
		}
		if s.TokensPerSec > maxSpeed {
			maxSpeed = s.TokensPerSec
		}
		if s.OutputTokens > maxOut {
			maxOut = s.OutputTokens
		}
	}
	minSpeed = max(0, minSpeed*0.8)
	maxSpeed = max(minSpeed+10, maxSpeed*1.1)

	const spanSec = 3600.0
	const inset float32 = 6
	prevID := *hoveredID
	nextID := 0
	if box.Hovered() {
		if px, py, ok := box.PointerPosition(); ok {
			w := PopoverWidth - PanelPadH*2
			plotW := max(1, w-inset*2)
			bestDist := float32(24 * 24)
			for _, s := range samples {
				age := max(0.0, min(spanSec, now.Sub(s.Timestamp).Seconds()))
				sx := inset + plotW*float32(1.0-age/spanSec)
				normY := float32((s.TokensPerSec - minSpeed) / (maxSpeed - minSpeed))
				sy := 64 - 7 - normY*(64-14)
				d := (px-sx)*(px-sx) + (py-sy)*(py-sy)
				if d < bestDist {
					bestDist = d
					nextID = s.ID
				}
			}
		}
	}
	if nextID != prevID {
		*hoveredID = nextID
		c.Invalidate()
	}
	if box.Clicked() && onClick != nil {
		onClick()
	}

	curHover := *hoveredID
	latestID := samples[len(samples)-1].ID
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		plotW := max(1, r.W-inset*2)
		for _, frac := range []float32{0.15, 0.5, 0.85} {
			gy := r.Y + r.H*frac
			p.Fill(ui.Rect{X: r.X + 4, Y: gy, W: r.W - 8, H: 0.5}, ui.RGBA(255, 255, 255, 0.05), 0)
		}
		for _, s := range samples {
			age := max(0.0, min(spanSec, now.Sub(s.Timestamp).Seconds()))
			cx := r.X + inset + plotW*float32(1.0-age/spanSec)
			normY := float32((s.TokensPerSec - minSpeed) / (maxSpeed - minSpeed))
			cy := r.Y + r.H - 7 - normY*(r.H-14)
			sizeRatio := math.Sqrt(float64(s.OutputTokens) / float64(maxOut))
			rad := float32(2.25 + sizeRatio*2.5)
			col := ui.RGBA(255, 255, 255, float32(0.25+0.45*(1.0-age/spanSec)))
			if s.ID == curHover || s.ID == latestID {
				col = ColorAGAccent
			}
			if s.ID == curHover {
				rad += 1.5
			}
			p.Fill(ui.Rect{X: cx - rad, Y: cy - rad, W: rad * 2, H: rad * 2}, col, rad)
		}
	})
}
