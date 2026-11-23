package appui

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"ccmonitor/internal/core"
	"ccmonitor/internal/service"
)

const PillHeight = 38

type PillActions struct {
	OnOpenPanel    func()
	SetAlwaysOnTop func(bool)
	ResetPosition  func()
	HidePill       func()
	StartDrag      func(dx, dy int)
}

// FloatingPillModel renders the optional Windows compact desktop strip
// replicating the macOS menu bar item (Clawd + shield + multi-column metrics).
// Single-click anywhere opens the main panel; holding left mouse button and
// moving >= 4px hands off to native OS window dragging; right-click opens menu.
type FloatingPillModel struct {
	Store   *service.Store
	Actions PillActions

	pressing  bool
	dragMoved bool
	dragAccX  float32
	dragAccY  float32
}

func NewFloatingPillModel(store *service.Store, actions PillActions) *FloatingPillModel {
	return &FloatingPillModel{
		Store:   store,
		Actions: actions,
	}
}

// PillTargetWidth estimates a clean window width (in DIPs) for the enabled metrics.
func PillTargetWidth(st service.ViewState) int {
	cols := 0
	if st.Prefs.PillShowFiveHour {
		cols++
	}
	if st.Prefs.PillShowWeek {
		cols++
	}
	if st.Prefs.PillShowScoped && st.OfficialUsage != nil && len(st.OfficialUsage.Scoped) > 0 {
		cols++
	}
	if st.Prefs.PillShowTodayCost {
		cols++
	}
	if st.Prefs.PillShowWeekCost && st.Snapshot != nil && st.Snapshot.Week != nil {
		cols++
	}
	if st.Prefs.PillShowMonthCost && st.Snapshot != nil {
		cols++
	}
	if st.Prefs.PillShowSpeed {
		cols++
	}
	if cols == 0 {
		return 64
	}
	w := 48 + cols*46
	if w < 120 {
		return 120
	}
	if w > 460 {
		return 460
	}
	return w
}

func (m *FloatingPillModel) View(c *ui.Context) {
	ApplyDarkTheme(c)
	c.Root().Background(ui.Transparent)

	st := m.Store.ViewState()
	zh := st.Prefs.IsChinese()
	now := time.Now()
	money := core.NewMoneyFormat(st.Prefs.Currency)

	if st.Prefs.ClawdAnimation {
		c.After(800 * time.Millisecond)
	} else if st.Prefs.PillShowSpeed {
		c.After(5 * time.Second)
	} else {
		c.After(15 * time.Second)
	}

	pill := ui.Row(c.Key("mini-pill")).Fill().Padding(4, 10).Gap(9).Radius(14).
		Background(ui.RGBA(34, 34, 32, 0.94)).Border(1, ColorSurfaceBorder).
		AlignItems(ui.Center).Cursor(ui.CursorPointer)

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

	pill.Children(func() {
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

		addCol := func(top, val string, vc ui.Color) {
			ui.Column(c).Center().Gap(0).Children(func() {
				ui.Text(c, top).FontSize(8.5).TextColor(ColorTertiary).SingleLine()
				ui.Text(c, val).FontSize(11).FontWeight(600).TextColor(vc).FontFeatures("tnum").SingleLine()
			})
		}

		if st.Prefs.PillShowFiveHour {
			v, vc := "–", ColorSecondary
			if st.OfficialUsage != nil && st.OfficialUsage.FiveHour != nil {
				eff := st.OfficialUsage.EffectiveAt(now)
				v = fmt.Sprintf("%d%%", eff.FiveHour.Percent())
				vc = LevelColor(eff.FiveHour.Fraction())
			}
			addCol(tr(zh, "5小时", "5h"), v, vc)
		}

		if st.Prefs.PillShowWeek {
			v, vc := "–", ColorSecondary
			if st.OfficialUsage != nil && st.OfficialUsage.SevenDay != nil {
				eff := st.OfficialUsage.EffectiveAt(now)
				v = fmt.Sprintf("%d%%", eff.SevenDay.Percent())
				vc = LevelColor(eff.SevenDay.Fraction())
			}
			addCol(tr(zh, "本周", "Week"), v, vc)
		}

		if st.Prefs.PillShowScoped && st.OfficialUsage != nil && len(st.OfficialUsage.Scoped) > 0 {
			eff := st.OfficialUsage.EffectiveAt(now)
			sc := eff.Scoped[0]
			addCol(sc.ModelName, fmt.Sprintf("%d%%", sc.Limit.Percent()), LevelColor(sc.Limit.Fraction()))
		}

		if st.Prefs.PillShowTodayCost {
			cost := 0.0
			if st.Snapshot != nil && st.Snapshot.Today() != nil {
				cost = st.Snapshot.Today().Cost
			}
			addCol(tr(zh, "今日", "Today"), money.Whole(cost), ColorText)
		}

		if st.Prefs.PillShowWeekCost && st.Snapshot != nil && st.Snapshot.Week != nil {
			addCol(tr(zh, "本周费", "Wk $"), money.Compact(st.Snapshot.Week.Cost), ColorText)
		}

		if st.Prefs.PillShowMonthCost && st.Snapshot != nil {
			addCol(tr(zh, "本月费", "Mo $"), money.Compact(st.Snapshot.Billing.Cost), ColorText)
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
			addCol(tr(zh, "速度", "TOK/S"), v, ColorText)
		}
	})
}
