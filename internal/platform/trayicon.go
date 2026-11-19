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
