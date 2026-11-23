package appui

import (
	"github.com/egoist/mygo/ui"

	"ccmonitor/internal/core"
)

// Exact visual palette and metrics from kittors/claude-usage-monitor (Theme.swift).
var (
	ColorAccent     = ui.Hex("#D97757")
	ColorAccentSoft = ui.Hex("#D97757").Alpha(0.20)
	ColorText       = ui.RGBA(255, 255, 255, 0.92)
	ColorSecondary  = ui.RGBA(255, 255, 255, 0.58)
	ColorTertiary   = ui.RGBA(255, 255, 255, 0.38)
	ColorQuaternary = ui.RGBA(255, 255, 255, 0.20)

	ColorTrack   = ui.RGBA(255, 255, 255, 0.09)
	ColorDivider = ui.RGBA(255, 255, 255, 0.08)
	ColorHover   = ui.RGBA(255, 255, 255, 0.055)
	ColorPillBg  = ui.RGBA(255, 255, 255, 0.05)
	ColorPillSel = ui.RGBA(255, 255, 255, 0.13)

	ColorSurfaceDark     = ui.Hex("#262624")
	ColorSurfacePanel    = ui.Hex("#2A2A28")
	ColorSurfaceSettings = ui.Hex("#232321")
	ColorSurfaceHeader   = ui.Hex("#2C2C2A")
	ColorSurfaceBorder   = ui.RGBA(255, 255, 255, 0.10)

	ColorWarning  = ui.Hex("#E3A24B")
	ColorCritical = ui.Hex("#E5584F")
	ColorLive     = ui.Hex("#6CC08B")

	ColorCacheRead  = ColorAccent
	ColorCacheWrite = ColorAccent.Alpha(0.50)
	ColorInputTok   = ui.RGBA(255, 255, 255, 0.62)
	ColorOutputTok  = ui.RGBA(255, 255, 255, 0.32)

	// Antigravity brand palette (Theme.swift)
	ColorAGAccent    = ui.Hex("#4F8CFF")
	ColorAGStarTop   = ui.Hex("#7AB8FF")
	ColorAGStarBot   = ui.Hex("#7361FA")
	ColorAGSafeGreen = ui.Hex("#5EC27B")
)

type ProviderPalette struct {
	Accent     ui.Color
	AccentSoft ui.Color
	CacheRead  ui.Color
	CacheWrite ui.Color
}

func PaletteFor(p core.ProviderKind) ProviderPalette {
	if core.NormalizeProvider(p) == core.ProviderAntigravity {
		return ProviderPalette{
			Accent:     ColorAGAccent,
			AccentSoft: ColorAGAccent.Alpha(0.20),
			CacheRead:  ColorAGAccent,
			CacheWrite: ColorAGAccent.Alpha(0.50),
		}
	}
	return ProviderPalette{
		Accent:     ColorAccent,
		AccentSoft: ColorAccentSoft,
		CacheRead:  ColorCacheRead,
		CacheWrite: ColorCacheWrite,
	}
}

func AGLevelColor(fraction float64, overPace bool) ui.Color {
	switch {
	case overPace || fraction >= 0.90:
		return ui.Hex("#E5584F")
	case fraction >= 0.75:
		return ui.Hex("#E57A4B")
	case fraction >= 0.55:
		return ui.Hex("#E3A24B")
	case fraction >= 0.35:
		return ui.Hex("#9EC95A")
	default:
		return ColorAGSafeGreen
	}
}

const (
	PopoverWidth  float32 = 356
	PopoverRadius float32 = 14
	PanelPadH     float32 = 16
)

func LevelColor(fraction float64) ui.Color {
	hex := core.ToneFor(fraction).Hex()
	return ui.RGB(uint8(hex>>16), uint8((hex>>8)&0xFF), uint8(hex&0xFF))
}

func ApplyDarkTheme(c *ui.Context) {
	ApplyThemeFor(c, core.ProviderClaude)
}

func ApplyThemeFor(c *ui.Context, p core.ProviderKind) {
	pal := PaletteFor(p)
	t := ui.DarkTheme().WithAccent(pal.Accent)
	t.Background = ColorSurfaceDark
	t.Surface = ColorSurfacePanel
	t.SurfaceHover = ColorHover
	t.Border = ColorSurfaceBorder
	t.Text = ColorText
	t.TextMuted = ColorSecondary
	t.Inverse = ui.Hex("#1A1A18")
	t.InverseText = ColorText
	t.Radius = 8
	t.TooltipDelay = 300_000_000 // 300ms
	t.InactiveAccent = pal.Accent
	c.SetTheme(t)
}
