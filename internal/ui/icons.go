package appui

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"ccmonitor/internal/platform"
)

const claudeLogoPathData = "M52.4285 162.873L98.7844 136.879L99.5485 134.602L98.7844 133.334H96.4921L88.7237 132.862L62.2346 132.153L39.3113 131.207L17.0249 130.026L11.4214 128.844L6.2 121.873L6.7094 118.447L11.4214 115.257L18.171 115.847L33.0711 116.911L55.485 118.447L71.6586 119.392L95.728 121.873H99.5485L100.058 120.337L98.7844 119.392L97.7656 118.447L74.5877 102.732L49.4995 86.1905L36.3823 76.62L29.3779 71.7757L25.8121 67.2858L24.2839 57.3608L30.6515 50.2716L39.3113 50.8623L41.4763 51.4531L50.2636 58.1879L68.9842 72.7209L93.4357 90.6804L97.0015 93.6343L98.4374 92.6652L98.6571 91.9801L97.0015 89.2625L83.757 65.2772L69.621 40.8192L63.2534 30.6579L61.5978 24.632C60.9565 22.1032 60.579 20.0111 60.579 17.4246L67.8381 7.49965L71.9133 6.19995L81.7193 7.49965L85.7946 11.0443L91.9074 24.9865L101.714 46.8451L116.996 76.62L121.453 85.4816L123.873 93.6343L124.764 96.1155H126.292V94.6976L127.566 77.9197L129.858 57.3608L132.15 30.8942L132.915 23.4505L136.608 14.4708L143.994 9.62643L149.725 12.344L154.437 19.0788L153.8 23.4505L150.998 41.6463L145.522 70.1215L141.957 89.2625H143.994L146.414 86.7813L156.093 74.0206L172.266 53.698L179.398 45.6635L187.803 36.802L193.152 32.5484H203.34L210.726 43.6549L207.415 55.1159L196.972 68.3492L188.312 79.5739L175.896 96.2095L168.191 109.585L168.882 110.689L170.738 110.53L198.755 104.504L213.91 101.787L231.994 98.7149L240.144 102.496L241.036 106.395L237.852 114.311L218.495 119.037L195.826 123.645L162.07 131.592L161.696 131.893L162.137 132.547L177.36 133.925L183.855 134.279H199.774L229.447 136.524L237.215 141.605L241.8 147.867L241.036 152.711L229.065 158.737L213.019 154.956L175.45 145.977L162.587 142.787H160.805V143.85L171.502 154.366L191.242 172.089L215.82 195.011L217.094 200.682L213.91 205.172L210.599 204.699L188.949 188.394L180.544 181.069L161.696 165.118H160.422V166.772L164.752 173.152L187.803 207.771L188.949 218.405L187.294 221.832L181.308 223.959L174.813 222.777L161.187 203.754L147.305 182.486L136.098 163.345L134.745 164.2L128.075 235.42L125.019 239.082L117.887 241.8L111.902 237.31L108.718 229.984L111.902 215.452L115.722 196.547L118.779 181.541L121.58 162.873L123.291 156.636L123.14 156.219L121.773 156.449L107.699 175.752L86.304 204.699L69.3663 222.777L65.291 224.431L58.2867 220.768L58.9235 214.27L62.8713 208.48L86.304 178.705L100.44 160.155L109.551 149.507L109.462 147.967L108.959 147.924L46.6977 188.512L35.6182 189.93L30.7788 185.44L31.4156 178.115L33.7079 175.752L52.4285 162.873Z"

func wrapIconSVG(body string) *ui.SVG {
	src := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">%s</svg>`, body)
	return ui.MustParseSVG([]byte(src))
}

var (
	ClaudeLogoPath = ui.MustParsePath(claudeLogoPathData)

	IconSliders = wrapIconSVG(`<path d="M4 7.5h8.4M16.8 7.5H20M4 16.5h3.2M11.6 16.5H20"/><circle cx="14.6" cy="7.5" r="2.2"/><circle cx="9.4" cy="16.5" r="2.2"/>`)
	IconRefresh = wrapIconSVG(`<path d="M19.6 10.6A7.8 7.8 0 0 0 6.1 7.1L4.4 8.8"/><path d="M4.4 4.6v4.2h4.2"/><path d="M4.4 13.4a7.8 7.8 0 0 0 13.5 3.5l1.7-1.7"/><path d="M19.6 19.4v-4.2h-4.2"/>`)
	IconPower   = wrapIconSVG(`<path d="M12 3.4v8"/><path d="M7.2 6.2a7.8 7.8 0 1 0 9.6 0"/>`)
	IconGauge   = wrapIconSVG(`<path d="M4.3 18a8.6 8.6 0 1 1 15.4 0"/><path d="M12 6.4v1.4M6.5 8.7l1 1M17.5 8.7l-1 1"/><path d="m12 14.2 3.6-4.4"/><circle cx="12" cy="14.2" r="1.9" fill="currentColor" stroke="none"/>`)
	IconMenuBar = wrapIconSVG(`<rect x="3" y="4.4" width="18" height="15.2" rx="3.4"/><path d="M3 8.6h18"/><circle cx="16.2" cy="6.5" r=".9" fill="currentColor" stroke="none"/><circle cx="18.4" cy="6.5" r=".9" fill="currentColor" stroke="none"/>`)
	IconPanel   = wrapIconSVG(`<path d="M3.4 4.2h17.2"/><rect x="5.6" y="7.2" width="12.8" height="13.4" rx="3"/><path d="M8.8 11.4h6.4"/><path d="M8.8 15h4"/>`)
	IconDB      = wrapIconSVG(`<ellipse cx="12" cy="6" rx="7.4" ry="2.9"/><path d="M4.6 6v12c0 1.6 3.3 2.9 7.4 2.9s7.4-1.3 7.4-2.9V6"/><path d="M4.6 12c0 1.6 3.3 2.9 7.4 2.9s7.4-1.3 7.4-2.9"/>`)
	IconClock   = wrapIconSVG(`<circle cx="12" cy="12" r="8.6"/><path d="M12 7.4V12l3.1 2"/>`)
	IconWarning = wrapIconSVG(`<path d="M10.3 4.4 3 17.3a2 2 0 0 0 1.7 3h14.6a2 2 0 0 0 1.7-3L13.7 4.4a2 2 0 0 0-3.4 0Z"/><path d="M12 9.6v4.2"/><circle cx="12" cy="16.9" r="1" fill="currentColor" stroke="none"/>`)
	IconTerm    = wrapIconSVG(`<rect x="3.2" y="4.6" width="17.6" height="14.8" rx="3.6"/><path d="m7.6 9.8 2.6 2.4-2.6 2.4"/><path d="M12.8 14.6h3.8"/>`)
	IconCal     = wrapIconSVG(`<rect x="3.5" y="5" width="17" height="15.5" rx="3.6"/><path d="M3.5 9.8h17"/><path d="M8 3v3.6M16 3v3.6"/><rect x="7" y="13" width="3.4" height="3.4" rx="1" fill="currentColor" stroke="none"/><path d="M13.6 14.7h3.4"/>`)
	IconPulse   = wrapIconSVG(`<path d="M3 12.4h3.8l2.4-5.8 4.6 11.2 2.5-5.4H21"/>`)
	IconFolder  = wrapIconSVG(`<path d="M3.5 7.4c0-1.3 1-2.4 2.4-2.4h3.5c.6 0 1.2.3 1.6.7l1.2 1.4h5.9c1.3 0 2.4 1.1 2.4 2.4v7.7c0 1.3-1.1 2.4-2.4 2.4H5.9c-1.3 0-2.4-1.1-2.4-2.4Z"/><path d="M3.5 10.4h17"/>`)
	IconChevDn  = wrapIconSVG(`<path d="m6.5 9.5 5.5 5.5 5.5-5.5"/>`)
	IconChevRt  = wrapIconSVG(`<path d="m9.5 6.5 5.5 5.5-5.5 5.5"/>`)
	IconCheck   = wrapIconSVG(`<path d="m5 12.5 4.5 4.5L19 7.5"/>`)
	IconGlobe   = wrapIconSVG(`<circle cx="12" cy="12" r="8.6"/><path d="M3.6 12h16.8"/><path d="M12 3.4c2.2 2.3 3.4 5.2 3.4 8.6s-1.2 6.3-3.4 8.6c-2.2-2.3-3.4-5.2-3.4-8.6S9.8 5.7 12 3.4Z"/>`)
	IconShield  = wrapIconSVG(`<path d="M12 3.2 5 6.2v5.4c0 4.5 3 8 7 9.2 4-1.2 7-4.7 7-9.2V6.2Z"/><path d="m9.2 12.2 2 2 3.8-4.2"/>`)
)

// ClaudeLogo draws the official 248x248 Claude sunburst vector at any size.
func ClaudeLogo(c *ui.Context, size float32, col ui.Color) ui.Element {
	return ui.Box(c).Size(size, size).Draw(func(p *ui.Painter, r ui.Rect) {
		scale := r.W / 248.0
		p.Transform(ui.Scaling(scale, scale).Then(ui.Translation(r.X, r.Y)), 0, 0, func() {
			p.FillPath(ClaudeLogoPath, col)
		})
	})
}

// AntigravityLogo draws the 30-degree tilted gradient 4-point star brand mark.
func AntigravityLogo(c *ui.Context, size float32, spinDeg float32) ui.Element {
	return ui.Box(c).Size(size, size).Draw(func(p *ui.Painter, r ui.Rect) {
		cx := r.X + r.W/2
		cy := r.Y + r.H/2
		rad := min(r.W, r.H) / 2
		ctrl := rad * 0.14

		var path ui.Path
		path.MoveTo(cx, cy-rad)
		path.QuadTo(cx+ctrl, cy-ctrl, cx+rad, cy)
		path.QuadTo(cx+ctrl, cy+ctrl, cx, cy+rad)
		path.QuadTo(cx-ctrl, cy+ctrl, cx-rad, cy)
		path.QuadTo(cx-ctrl, cy-ctrl, cx, cy-rad)
		path.Close()

		grad := ui.LinearGradient{
			Angle: 135,
			Stops: []ui.GradientStop{
				{Color: ColorAGStarTop, At: 0},
				{Color: ColorAGAccent, At: 0.55},
				{Color: ColorAGStarBot, At: 1},
			},
		}
		p.Transform(ui.Rotation(30+spinDeg), cx, cy, func() {
			p.FillPathGradient(&path, grad)
		})
	})
}

// Clawd pixel masks (5 rows x 17 cols, matching BrandAssets.swift)
var clawdPoses = map[string][5]string{
	"idle": {
		"  XXXXXXXXXXXXX  ",
		"  XX XXXXXXX XX  ",
		"XXXXXXXXXXXXXXXXX",
		"  XXXXXXXXXXXXX  ",
		"   XX       XX   ",
	},
	"left": {
		"  XXXXXXXXXXXXX  ",
		"  X XXXXXXX X X  ",
		"XXXXXXXXXXXXXXXXX",
		"  XXXXXXXXXXXXX  ",
		"   XX       XX   ",
	},
	"right": {
		"  XXXXXXXXXXXXX  ",
		"  XXX XXXXXXX X  ",
		"XXXXXXXXXXXXXXXXX",
		"  XXXXXXXXXXXXX  ",
		"   XX       XX   ",
	},
	"blink": {
		"  XXXXXXXXXXXXX  ",
		"  XXXXXXXXXXXXX  ",
		"XXXXXXXXXXXXXXXXX",
		"  XXXXXXXXXXXXX  ",
		"   XX       XX   ",
	},
}

// ClawdMascot draws the pixel Clawd mascot (with optional exit shield beside it).
func ClawdMascot(c *ui.Context, width float32, col ui.Color, pose string, shield platform.ShieldState) ui.Element {
	height := width / (17.0 / (5.0 * 2.2))
	totalW := width
	if shield != platform.ShieldNone {
		totalW += height * 0.65
	}
	mask, ok := clawdPoses[pose]
	if !ok {
		mask = clawdPoses["idle"]
	}
	return ui.Box(c).Size(totalW, height).Draw(func(p *ui.Painter, r ui.Rect) {
		unitW := width / 17.0
		unitH := height / 5.0
		for rowIdx, row := range mask {
			colIdx := 0
			for colIdx < len(row) {
				if row[colIdx] != 'X' {
					colIdx++
					continue
				}
				start := colIdx
				for colIdx < len(row) && row[colIdx] == 'X' {
					colIdx++
				}
				p.Fill(ui.Rect{
					X: r.X + float32(start)*unitW,
					Y: r.Y + float32(rowIdx)*unitH,
					W: float32(colIdx-start) * unitW,
					H: unitH,
				}, col, 0)
			}
		}
		if shield != platform.ShieldNone {
			scol := col
			if shield == platform.ShieldRisk || shield == platform.ShieldSevere || shield == platform.ShieldLoading {
				scol = ColorCritical
			}
			sx := r.X + width + 2
			sy := r.Y + height*0.18
			sw := height * 0.56
			sh := height * 0.68
			var sp ui.Path
			sp.MoveTo(sx+sw/2, sy)
			sp.LineTo(sx+sw, sy+sh*0.22)
			sp.LineTo(sx+sw, sy+sh*0.58)
			sp.LineTo(sx+sw/2, sy+sh)
			sp.LineTo(sx, sy+sh*0.58)
			sp.LineTo(sx, sy+sh*0.22)
			sp.Close()
			p.FillPath(&sp, scol)
			// Draw tiny check or exclamation inside shield
			inner := ColorSurfaceDark
			if shield == platform.ShieldSafe {
				var ck ui.Path
				ck.MoveTo(sx+sw*0.26, sy+sh*0.50)
				ck.LineTo(sx+sw*0.44, sy+sh*0.67)
				ck.LineTo(sx+sw*0.76, sy+sh*0.35)
				p.StrokePath(&ck, 1.3, inner)
			} else {
				var ex ui.Path
				ex.MoveTo(sx+sw*0.5, sy+sh*0.28)
				ex.LineTo(sx+sw*0.5, sy+sh*0.56)
				p.StrokePath(&ex, 1.3, inner)
				p.Fill(ui.Rect{X: sx + sw*0.5 - 0.7, Y: sy + sh*0.68, W: 1.4, H: 1.4}, inner, 0.7)
			}
		}
	})
}

// CountryBadge draws a crisp miniature flag badge (e.g. US flag or two-letter region tag),
// since Windows DirectWrite does not render Unicode regional-indicator flag emojis in color.
func CountryBadge(c *ui.Context, code string) ui.Element {
	return ui.Box(c).Size(18, 13).Radius(2).Clip().Draw(func(p *ui.Painter, r ui.Rect) {
		switch code {
		case "US":
			// 7 red/white stripes + blue canton
			for i := 0; i < 7; i++ {
				col := ui.Hex("#D94A4A")
				if i%2 == 1 {
					col = ui.Hex("#EAEAEA")
				}
				p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H*float32(i)/7.0, W: r.W, H: r.H/7.0 + 0.5}, col, 0)
			}
			p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W * 0.48, H: r.H * 0.55}, ui.Hex("#2B4C8C"), 0)
		case "JP":
			p.Fill(r, ui.Hex("#F2F2F2"), 2)
			var dot ui.Path
			dot.MoveTo(r.X+r.W/2+3.5, r.Y+r.H/2)
			p.Fill(ui.Rect{X: r.X + r.W/2 - 3.2, Y: r.Y + r.H/2 - 3.2, W: 6.4, H: 6.4}, ui.Hex("#D13438"), 3.2)
		case "CN":
			p.Fill(r, ui.Hex("#D13438"), 2)
			p.Fill(ui.Rect{X: r.X + 2.5, Y: r.Y + 2.5, W: 3.5, H: 3.5}, ui.Hex("#F5C542"), 1.5)
		case "HK", "MO":
			p.Fill(r, ui.Hex("#D13438"), 2)
			p.Fill(ui.Rect{X: r.X + r.W/2 - 2.5, Y: r.Y + r.H/2 - 2.5, W: 5, H: 5}, ui.Hex("#F5F5F5"), 2.5)
		case "SG":
			p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H / 2}, ui.Hex("#D93B3B"), 0)
			p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H/2, W: r.W, H: r.H / 2}, ui.Hex("#EFEFEF"), 0)
		case "GB":
			p.Fill(r, ui.Hex("#244382"), 2)
			p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H/2 - 1.5, W: r.W, H: 3}, ui.Hex("#EAEAEA"), 0)
			p.Fill(ui.Rect{X: r.X + r.W/2 - 1.5, Y: r.Y, W: 3, H: r.H}, ui.Hex("#EAEAEA"), 0)
			p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H/2 - 0.8, W: r.W, H: 1.6}, ui.Hex("#D13438"), 0)
			p.Fill(ui.Rect{X: r.X + r.W/2 - 0.8, Y: r.Y, W: 1.6, H: r.H}, ui.Hex("#D13438"), 0)
		default:
			p.Fill(r, ui.RGBA(255, 255, 255, 0.12), 2)
			sp := ui.Span{Text: code, Size: 8, Weight: 600, Color: ColorSecondary}
			tw, th := p.MeasureText(0, sp)
			p.RichText(r.X+(r.W-tw)/2, r.Y+(r.H-th)/2, 0, sp)
		}
	})
}
