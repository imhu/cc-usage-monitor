package appui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"ccmonitor/internal/core"
	"ccmonitor/internal/service"
)

const (
	PillBodyHeight      = 38
	PillShadowPadX      = 12
	PillShadowPadTop    = 9
	PillShadowPadBottom = 15
	PillHeight          = PillBodyHeight + PillShadowPadTop + PillShadowPadBottom
	PillMinWidth        = 44 + 2*PillShadowPadX
	PillMaxWidth        = 520 + 2*PillShadowPadX
)

type PillActions struct {
	OnOpenPanel       func()
	SetAlwaysOnTop    func(bool)
	ResetPosition     func()
	HidePill          func()
	StartDrag         func(dx, dy int)
	OnProviderChanged func()
	OnContentWidth    func(width int)
}

// FloatingPillModel renders the optional Windows compact desktop strip
// replicating the macOS menu bar item (Clawd + shield + multi-column metrics).
// Single-click anywhere opens the main panel; holding left mouse button and
// moving >= 4px hands off to native OS window dragging; right-click opens menu.
type FloatingPillModel struct {
	Store   *service.Store
	Actions PillActions

	pressing       bool
	dragMoved      bool
	dragAccX       float32
	dragAccY       float32
	lastContentW   int
	lastContentSig string
}

func NewFloatingPillModel(store *service.Store, actions PillActions) *FloatingPillModel {
	return &FloatingPillModel{
		Store:   store,
		Actions: actions,
	}
}

func (m *FloatingPillModel) ResetMeasuredWidth() {
	m.lastContentW = 0
	m.lastContentSig = ""
}

type pillCol struct {
	top string
	val string
	col ui.Color
}

func estimatePillTextWidth(s string, fontSize float64) float64 {
	var w float64
	for _, r := range s {
		switch {
		case r > 0x7F:
			w += fontSize * 1.02
		case r == '.' || r == ':' || r == ',' || r == ' ':
			w += fontSize * 0.35
		default:
			w += fontSize * 0.62
		}
	}
	return w
}

// PillTargetWidth computes a tight initial window width (in DIPs) for the enabled metrics.
func PillTargetWidth(st service.ViewState) int {
	zh := st.Prefs.IsChinese()
	now := time.Now()
	money := core.NewMoneyFormatWithRates(st.Prefs.Currency, st.FXRates)
	provider := core.NormalizeProvider(st.Prefs.ActiveProvider)

	const chromeW = 24.0
	const gap = 9.0

	if provider == core.ProviderAntigravity {
		cols := buildAGPillColumns(st, money, zh)
		const iconW = 16.0
		if len(cols) == 0 {
			return PillMinWidth
		}
		var w float64
		if len(cols) == 1 {
			w = chromeW + iconW + gap + estimatePillTextWidth(cols[0].val, 12.5)
		} else {
			w = chromeW + iconW + gap*float64(len(cols))
			for _, col := range cols {
				topW := estimatePillTextWidth(col.top, 8.5)
				valW := estimatePillTextWidth(col.val, 11.0)
				w += max(topW, valW)
			}
		}
		return max(PillMinWidth, min(PillMaxWidth, int(math.Ceil(w))+2*PillShadowPadX))
	}

	cols := buildClaudePillColumns(st, now, money, zh)
	iconW := 17.0
	if st.Prefs.IconStyle == core.IconStyleLogo {
		iconW = 15.0
	}
	if len(cols) == 0 {
		return PillMinWidth
	}
	w := chromeW + iconW + gap*float64(len(cols))
	for _, col := range cols {
		topW := estimatePillTextWidth(col.top, 8.5)
		valW := estimatePillTextWidth(col.val, 11.0)
		w += max(topW, valW)
	}
	return max(PillMinWidth, min(PillMaxWidth, int(math.Ceil(w))+2*PillShadowPadX))
}

func (m *FloatingPillModel) View(c *ui.Context) {
	st := m.Store.ViewState()
	provider := core.NormalizeProvider(st.Prefs.ActiveProvider)
	ApplyThemeFor(c, provider)
	c.Root().Background(ui.Transparent).Padding(PillShadowPadTop, PillShadowPadX, PillShadowPadBottom, PillShadowPadX)

	zh := st.Prefs.IsChinese()
	now := time.Now()
	money := core.NewMoneyFormatWithRates(st.Prefs.Currency, st.FXRates)

	agExecuting := provider == core.ProviderAntigravity && st.Prefs.AGLogoAnimation && st.AG.Execution.IsActiveAt(now)

	if provider == core.ProviderClaude && st.Prefs.ClawdAnimation {
		c.After(800 * time.Millisecond)
	} else if provider == core.ProviderClaude && st.Prefs.PillShowSpeed {
		c.After(5 * time.Second)
	} else if agExecuting {
		c.After(1 * time.Second)
	} else {
		c.After(15 * time.Second)
	}

	pill := ui.Row(c.Key("mini-pill")).Fill().Padding(4, 10).Radius(14).
		Background(ui.RGBA(34, 34, 32, 0.94)).Border(1, ColorSurfaceBorder).
		Shadow(0, 3, 10, 0, ui.RGBA(0, 0, 0, 0.30)).
		Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.20)).
		Center().Cursor(ui.CursorPointer)

	// Calling Dragged() captures pointer-move deltas while left button is held.
	dx, dy, hasDrag := pill.Dragged()
	pressed := pill.Pressed()
	clicked := pill.Clicked()

	wasDragged := false
	if pressed {
		if !m.pressing {
			m.pressing = true
			m.dragMoved = false
			m.dragAccX = 0
			m.dragAccY = 0
		}
		if hasDrag && !m.dragMoved {
			m.dragAccX += dx
			m.dragAccY += dy
			if math.Abs(float64(m.dragAccX)) >= 4 || math.Abs(float64(m.dragAccY)) >= 4 {
				m.dragMoved = true
				if m.Actions.StartDrag != nil {
					m.Actions.StartDrag(int(math.Round(float64(m.dragAccX))), int(math.Round(float64(m.dragAccY))))
				}
			}
		}
	} else if m.pressing {
		wasDragged = m.dragMoved
		m.pressing = false
		m.dragMoved = false
		m.dragAccX = 0
		m.dragAccY = 0
	}

	if clicked && !m.dragMoved && !wasDragged && m.Actions.OnOpenPanel != nil {
		m.Actions.OnOpenPanel()
	}

	pill.ContextMenu(func(menu *ui.Menu) {
		if menu.Item(tr(zh, "打开主面板", "Open Panel")).Chosen() && m.Actions.OnOpenPanel != nil {
			m.Actions.OnOpenPanel()
		}
		switchLabel := tr(zh, "切换到 Antigravity 用量", "Switch to Antigravity")
		nextProvider := core.ProviderAntigravity
		if provider == core.ProviderAntigravity {
			switchLabel = tr(zh, "切换到 Claude 用量", "Switch to Claude")
			nextProvider = core.ProviderClaude
		}
		if menu.Item(switchLabel).Chosen() {
			m.Store.SetActiveProvider(nextProvider)
			if m.Actions.OnProviderChanged != nil {
				m.Actions.OnProviderChanged()
			}
		}
		menu.Separator()
		if menu.Item(tr(zh, "始终置顶", "Always on top")).Checked(st.Prefs.PillAlwaysOnTop).Chosen() {
			next := !st.Prefs.PillAlwaysOnTop
			m.Store.UpdatePrefs(func(p *core.Preferences) { p.PillAlwaysOnTop = next })
			if m.Actions.SetAlwaysOnTop != nil {
				m.Actions.SetAlwaysOnTop(next)
			}
		}
		if menu.Item(tr(zh, "重置位置", "Reset position")).Chosen() {
			m.Store.UpdatePrefs(func(p *core.Preferences) {
				p.PillPosX = nil
				p.PillPosY = nil
			})
			if m.Actions.ResetPosition != nil {
				m.Actions.ResetPosition()
			}
		}
		menu.Separator()
		if menu.Item(tr(zh, "隐藏悬浮条", "Hide floating bar")).Chosen() {
			m.Store.UpdatePrefs(func(p *core.Preferences) { p.ShowFloatingPill = false })
			if m.Actions.HidePill != nil {
				m.Actions.HidePill()
			}
		}
	})

	var cols []pillCol
	if provider == core.ProviderAntigravity {
		cols = buildAGPillColumns(st, money, zh)
	} else {
		cols = buildClaudePillColumns(st, now, money, zh)
	}
	sig := pillSignature(provider, zh, st.Prefs.IconStyle, cols)

	pill.Children(func() {
		content := ui.Row(c.Key("mini-pill-content")).Gap(9).AlignItems(ui.Center).Shrink(0)
		content.Children(func() {
			if provider == core.ProviderAntigravity {
				AntigravityLogoAnimated(c, "ag-pill-logo", 16, agExecuting)
				if len(cols) == 1 {
					ui.Text(c, cols[0].val).FontSize(12.5).FontWeight(600).TextColor(cols[0].col).FontFeatures("tnum").SingleLine()
				} else {
					renderPillColumns(c, cols)
				}
				return
			}

			col := ColorLive
			if st.OfficialUsage != nil && st.OfficialUsage.FiveHour != nil {
				eff := st.OfficialUsage.EffectiveAt(now)
				col = LevelColor(eff.FiveHour.Fraction())
			}
			if st.Prefs.IconStyle == core.IconStyleLogo {
				ClaudeLogo(c, 15, col)
			} else {
				pose := "idle"
				if st.Prefs.ClawdAnimation {
					sec := now.Unix() % 12
					if sec == 4 {
						pose = "right"
					} else if sec == 5 {
						pose = "left"
					} else if sec == 9 {
						pose = "blink"
					}
				}
				ClawdMascot(c, 17, col, pose, st.Shield)
			}
			renderPillColumns(c, cols)
		})

		if m.Actions.OnContentWidth != nil {
			b := content.LayoutBounds()
			if sig != m.lastContentSig || b.W <= 0 {
				m.lastContentSig = sig
				c.After(16 * time.Millisecond)
			} else {
				wantW := int(math.Ceil(float64(b.W))) + 24 + 2*PillShadowPadX
				if wantW < PillMinWidth {
					wantW = PillMinWidth
				}
				if wantW > PillMaxWidth {
					wantW = PillMaxWidth
				}
				if wantW != m.lastContentW {
					m.lastContentW = wantW
					m.Actions.OnContentWidth(wantW)
				}
			}
		}
	})
}

func renderPillColumns(c *ui.Context, cols []pillCol) {
	for _, col := range cols {
		ui.Column(c).Center().Gap(0).Children(func() {
			ui.Text(c, col.top).FontSize(8.5).TextColor(ColorTertiary).SingleLine()
			ui.Text(c, col.val).FontSize(11).FontWeight(600).TextColor(col.col).FontFeatures("tnum").SingleLine()
		})
	}
}

func pillSignature(provider core.ProviderKind, zh bool, iconStyle core.IconStyle, cols []pillCol) string {
	var sb strings.Builder
	sb.WriteString(string(provider))
	if zh {
		sb.WriteString("|zh|")
	} else {
		sb.WriteString("|en|")
	}
	sb.WriteString(string(iconStyle))
	for _, col := range cols {
		sb.WriteByte('|')
		sb.WriteString(col.top)
		sb.WriteByte('=')
		sb.WriteString(col.val)
	}
	return sb.String()
}

func buildClaudePillColumns(st service.ViewState, now time.Time, money core.MoneyFormat, zh bool) []pillCol {
	var cols []pillCol

	if st.Prefs.PillShowFiveHour {
		v, vc := "–", ColorSecondary
		if st.OfficialUsage != nil && st.OfficialUsage.FiveHour != nil {
			eff := st.OfficialUsage.EffectiveAt(now)
			v = fmt.Sprintf("%d%%", eff.FiveHour.Percent())
			vc = LevelColor(eff.FiveHour.Fraction())
		}
		cols = append(cols, pillCol{tr(zh, "5小时", "5h"), v, vc})
	}

	if st.Prefs.PillShowWeek {
		v, vc := "–", ColorSecondary
		if st.OfficialUsage != nil && st.OfficialUsage.SevenDay != nil {
			eff := st.OfficialUsage.EffectiveAt(now)
			v = fmt.Sprintf("%d%%", eff.SevenDay.Percent())
			vc = LevelColor(eff.SevenDay.Fraction())
		}
		cols = append(cols, pillCol{tr(zh, "本周", "Week"), v, vc})
	}

	if st.Prefs.PillShowScoped && st.OfficialUsage != nil && len(st.OfficialUsage.Scoped) > 0 {
		eff := st.OfficialUsage.EffectiveAt(now)
		sc := eff.Scoped[0]
		cols = append(cols, pillCol{sc.ModelName, fmt.Sprintf("%d%%", sc.Limit.Percent()), LevelColor(sc.Limit.Fraction())})
	}

	if st.Prefs.PillShowTodayCost {
		cost := 0.0
		if st.Snapshot != nil && st.Snapshot.Today() != nil {
			cost = st.Snapshot.Today().Cost
		}
		cols = append(cols, pillCol{tr(zh, "今日", "Today"), money.Whole(cost), ColorText})
	}

	if st.Prefs.PillShowWeekCost && st.Snapshot != nil && st.Snapshot.Week != nil {
		cols = append(cols, pillCol{tr(zh, "本周费", "Wk $"), money.Compact(st.Snapshot.Week.Cost), ColorText})
	}

	if st.Prefs.PillShowMonthCost && st.Snapshot != nil {
		cols = append(cols, pillCol{tr(zh, "本月费", "Mo $"), money.Compact(st.Snapshot.Billing.Cost), ColorText})
	}

	if st.Prefs.PillShowSpeed {
		nowSec := float64(now.Unix()) + float64(now.Nanosecond())/1e9
		models := st.SpeedLog.Models(nowSec)
		v := "–"
		if cur := st.ActiveSpeedModel(models); cur != nil {
			if r := core.ComputeSpeedRate(cur.Requests, nowSec-core.SpeedRecentWindow, nowSec); r != nil {
				v = fmt.Sprintf("%d", int(math.Round(r.TokensPerSecond())))
			}
		}
		cols = append(cols, pillCol{tr(zh, "速度", "TOK/S"), v, ColorText})
	}

	return cols
}

func buildAGPillColumns(st service.ViewState, money core.MoneyFormat, zh bool) []pillCol {
	off := st.AG.OfficialReport
	local := st.AG.LocalReport
	var cols []pillCol

	if st.Prefs.AGPillShowFiveHour {
		lbl := tr(zh, "5小时", "5H")
		if b := off.PrimaryFiveHour; b != nil {
			cols = append(cols, pillCol{lbl, fmt.Sprintf("%d%%", b.UsedPercentInt()), AGLevelColor(b.UsedFraction, b.IsOverPace())})
		} else {
			cols = append(cols, pillCol{lbl, "--", ColorSecondary})
		}
	}
	if st.Prefs.AGPillShowWeek {
		lbl := tr(zh, "本周", "WEEK")
		if b := off.PrimaryWeekly; b != nil {
			cols = append(cols, pillCol{lbl, fmt.Sprintf("%d%%", b.UsedPercentInt()), AGLevelColor(b.UsedFraction, b.IsOverPace())})
		} else {
			cols = append(cols, pillCol{lbl, "--", ColorSecondary})
		}
	}
	if st.Prefs.AGPillShowCache {
		lbl := tr(zh, "缓存", "CACHE")
		if local.Today.TotalInputTokens() > 0 {
			cols = append(cols, pillCol{lbl, core.FormatPercent(local.Today.CacheHitRate(), 0), ColorAGAccent})
		} else {
			cols = append(cols, pillCol{lbl, "--", ColorSecondary})
		}
	}
	if st.Prefs.AGPillShowTodayCost {
		cols = append(cols, pillCol{tr(zh, "今日", "TODAY"), money.Compact(local.Today.EstimatedCostUSD), ColorText})
	}
	if st.Prefs.AGPillShowContext {
		lbl := tr(zh, "上下文", "CTX")
		if usedPct, _, _, ok := core.ResolveAGContextWindow(off, local); ok {
			cols = append(cols, pillCol{lbl, fmt.Sprintf("%d%%", int(math.Round(usedPct))), AGLevelColor(usedPct/100.0, false)})
		} else {
			cols = append(cols, pillCol{lbl, "--", ColorSecondary})
		}
	}
	return cols
}
