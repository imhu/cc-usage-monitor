package platform

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"

	"ccmonitor/internal/core"
)

type ShieldState int

const (
	ShieldNone ShieldState = iota
	ShieldSafe
	ShieldRisk
	ShieldSevere
	ShieldLoading
)

// RenderTrayIcon draws a crisp 32x32 PNG tray icon with Clawd or Claude sunburst,
// optional usage color accent, and exit-shield status indicator.
func RenderTrayIcon(style core.IconStyle, percent int, hasUsage bool, shield ShieldState, darkTaskbar bool) []byte {
	const size = 32
	img := image.NewNRGBA(image.Rect(0, 0, size, size))

	ink := color.NRGBA{R: 0xD9, G: 0x77, B: 0x57, A: 255}
	if hasUsage {
		hex := core.ToneFor(float64(percent) / 100.0).Hex()
		ink = color.NRGBA{R: uint8(hex >> 16), G: uint8((hex >> 8) & 0xFF), B: uint8(hex & 0xFF), A: 255}
	}

	if style == core.IconStyleLogo {
		drawSunburst(img, 14, 16, 11.5, ink)
	} else {
		drawClawdPixels(img, 2, 7, 1.5, ink)
	}

	// Draw shield / status badge at bottom-right
	if shield != ShieldNone {
		var badge color.NRGBA
		switch shield {
		case ShieldSafe:
			badge = color.NRGBA{R: 0x6C, G: 0xC0, B: 0x8B, A: 255}
		case ShieldRisk, ShieldSevere, ShieldLoading:
			badge = color.NRGBA{R: 0xE5, G: 0x58, B: 0x4F, A: 255}
		}
		bg := color.NRGBA{R: 0x1E, G: 0x1E, B: 0x1C, A: 255}
		if !darkTaskbar {
			bg = color.NRGBA{R: 0xF5, G: 0xF5, B: 0xF5, A: 255}
		}
		fillCircle(img, 25, 25, 6.2, bg)
		fillCircle(img, 25, 25, 4.6, badge)
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// RenderAntigravityTrayIcon draws a crisp 32x32 PNG tray icon with the tilted
// gradient 4-point star and optional usage status badge.
func RenderAntigravityTrayIcon(percent int, hasUsage bool, overPace bool, darkTaskbar bool) []byte {
	const size = 32
	img := image.NewNRGBA(image.Rect(0, 0, size, size))

	cx, cy, radius := 16.0, 16.0, 13.5
	if hasUsage {
		cx, cy, radius = 14.2, 14.5, 12.2
	}
	drawFourPointStar(img, cx, cy, radius, 30.0*math.Pi/180.0)

	if hasUsage {
		badge := agLevelColor(float64(percent)/100.0, overPace)
		bg := color.NRGBA{R: 0x1E, G: 0x1E, B: 0x1C, A: 255}
		if !darkTaskbar {
			bg = color.NRGBA{R: 0xF5, G: 0xF5, B: 0xF5, A: 255}
		}
		fillCircle(img, 25, 25, 6.2, bg)
		fillCircle(img, 25, 25, 4.6, badge)
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func agLevelColor(fraction float64, overPace bool) color.NRGBA {
	switch {
	case overPace || fraction >= 0.90:
		return color.NRGBA{R: 0xE5, G: 0x58, B: 0x4F, A: 255}
	case fraction >= 0.75:
		return color.NRGBA{R: 0xE5, G: 0x7A, B: 0x4B, A: 255}
	case fraction >= 0.55:
		return color.NRGBA{R: 0xE3, G: 0xA2, B: 0x4B, A: 255}
	case fraction >= 0.35:
		return color.NRGBA{R: 0x9E, G: 0xC9, B: 0x5A, A: 255}
	default:
		return color.NRGBA{R: 0x5E, G: 0xC2, B: 0x7B, A: 255}
	}
}

type starPt struct{ x, y float64 }

func drawFourPointStar(img *image.NRGBA, cx, cy, r, rotRad float64) {
	// Sample the 4 quadratic bezier arcs matching FourPointStarShape (c = 0.14*r).
	c := r * 0.14
	quad := func(p0, pc, p1 starPt, steps int) []starPt {
		pts := make([]starPt, 0, steps)
		for i := 0; i < steps; i++ {
			t := float64(i) / float64(steps)
			u := 1.0 - t
			pts = append(pts, starPt{
				x: u*u*p0.x + 2*u*t*pc.x + t*t*p1.x,
				y: u*u*p0.y + 2*u*t*pc.y + t*t*p1.y,
			})
		}
		return pts
	}
	var poly []starPt
	poly = append(poly, quad(starPt{0, -r}, starPt{c, -c}, starPt{r, 0}, 14)...)
	poly = append(poly, quad(starPt{r, 0}, starPt{c, c}, starPt{0, r}, 14)...)
	poly = append(poly, quad(starPt{0, r}, starPt{-c, c}, starPt{-r, 0}, 14)...)
	poly = append(poly, quad(starPt{-r, 0}, starPt{-c, -c}, starPt{0, -r}, 14)...)

	cosA, sinA := math.Cos(rotRad), math.Sin(rotRad)
	for i, pt := range poly {
		poly[i] = starPt{
			x: cx + pt.x*cosA - pt.y*sinA,
			y: cy + pt.x*sinA + pt.y*cosA,
		}
	}

	cTop := [3]float64{0x7A, 0xB8, 0xFF}
	cMid := [3]float64{0x4F, 0x8C, 0xFF}
	cBot := [3]float64{0x73, 0x61, 0xFA}

	const sub = 4
	minX := max(0, int(math.Floor(cx-r-1)))
	maxX := min(img.Bounds().Dx()-1, int(math.Ceil(cx+r+1)))
	minY := max(0, int(math.Floor(cy-r-1)))
	maxY := min(img.Bounds().Dy()-1, int(math.Ceil(cy+r+1)))

	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			hits := 0
			for sy := 0; sy < sub; sy++ {
				for sx := 0; sx < sub; sx++ {
					px := float64(x) + (float64(sx)+0.5)/float64(sub)
					py := float64(y) + (float64(sy)+0.5)/float64(sub)
					if pointInStarPoly(px, py, poly) {
						hits++
					}
				}
			}
			if hits == 0 {
				continue
			}
			alpha := uint8(math.Round(255.0 * float64(hits) / float64(sub*sub)))
			// Linear gradient from top-left (-r,-r) to bottom-right (+r,+r)
			t := ((float64(x)+0.5-(cx-r)) + (float64(y)+0.5-(cy-r))) / (4.0 * r)
			if t < 0 {
				t = 0
			} else if t > 1 {
				t = 1
			}
			var rgb [3]float64
			if t <= 0.55 {
				f := t / 0.55
				for k := 0; k < 3; k++ {
					rgb[k] = cTop[k]*(1-f) + cMid[k]*f
				}
			} else {
				f := (t - 0.55) / 0.45
				for k := 0; k < 3; k++ {
					rgb[k] = cMid[k]*(1-f) + cBot[k]*f
				}
			}
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(math.Round(rgb[0])),
				G: uint8(math.Round(rgb[1])),
				B: uint8(math.Round(rgb[2])),
				A: alpha,
			})
		}
	}
}

func pointInStarPoly(px, py float64, poly []starPt) bool {
	inside := false
	j := len(poly) - 1
	for i := 0; i < len(poly); i++ {
		yi, yj := poly[i].y, poly[j].y
		if (yi > py) != (yj > py) {
			xint := (poly[j].x-poly[i].x)*(py-yi)/(yj-yi) + poly[i].x
			if px < xint {
				inside = !inside
			}
		}
		j = i
	}
	return inside
}

// 5x17 Clawd idle pixel mask matching BrandAssets.swift (columns 1..17, rows 0..4)
var clawdIdleMask = [5]string{
	"  XXXXXXXXXXXXX  ",
	"  XX XXXXXXX XX  ",
	"XXXXXXXXXXXXXXXXX",
	"  XXXXXXXXXXXXX  ",
	"   XX       XX   ",
}

func drawClawdPixels(img *image.NRGBA, ox, oy, unit float64, c color.NRGBA) {
	uh := unit * 2.1
	for r, row := range clawdIdleMask {
		for col := 0; col < len(row); col++ {
			if row[col] != 'X' {
				continue
			}
			x0 := int(math.Round(ox + float64(col)*unit))
			x1 := int(math.Round(ox + float64(col+1)*unit))
			y0 := int(math.Round(oy + float64(r)*uh))
			y1 := int(math.Round(oy + float64(r+1)*uh))
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					if image.Pt(x, y).In(img.Bounds()) {
						img.SetNRGBA(x, y, c)
					}
				}
			}
		}
	}
}

func drawSunburst(img *image.NRGBA, cx, cy, radius float64, c color.NRGBA) {
	// 12-ray sunburst evoking Claude's logo at 32px
	const rays = 12
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			dx := float64(x) + 0.5 - cx
			dy := float64(y) + 0.5 - cy
			dist := math.Hypot(dx, dy)
			if dist > radius {
				continue
			}
			if dist < radius*0.26 {
				img.SetNRGBA(x, y, c)
				continue
			}
			angle := math.Atan2(dy, dx)
			if angle < 0 {
				angle += 2 * math.Pi
			}
			sector := angle / (2 * math.Pi / rays)
			frac := math.Abs(sector - math.Round(sector))
			halfWidth := 0.16 * (1.0 - 0.35*(dist/radius))
			if frac <= halfWidth {
				img.SetNRGBA(x, y, c)
			}
		}
	}
}

func fillCircle(img *image.NRGBA, cx, cy, r float64, c color.NRGBA) {
	for y := int(math.Floor(cy - r - 1)); y <= int(math.Ceil(cy+r+1)); y++ {
		for x := int(math.Floor(cx - r - 1)); x <= int(math.Ceil(cx+r+1)); x++ {
			if !image.Pt(x, y).In(img.Bounds()) {
				continue
			}
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			if d <= r-0.5 {
				img.SetNRGBA(x, y, c)
			} else if d < r+0.5 {
				alpha := uint8(float64(c.A) * (r + 0.5 - d))
				img.SetNRGBA(x, y, color.NRGBA{R: c.R, G: c.G, B: c.B, A: alpha})
			}
		}
	}
}
