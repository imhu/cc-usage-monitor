package appui

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"ccmonitor/internal/core"
	"ccmonitor/internal/service"
)

// FloatingPillModel renders the optional Windows always-on-top compact strip
// replicating the macOS menu bar item in hero.png (Clawd + shield + multi-column metrics).
type FloatingPillModel struct {
	Store       *service.Store
	OnOpenPanel func()
}

func NewFloatingPillModel(store *service.Store, onOpenPanel func()) *FloatingPillModel {
	return &FloatingPillModel{
		Store:       store,
		OnOpenPanel: onOpenPanel,
	}
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
	} else {
		c.After(15 * time.Second)
	}

	pill := ui.Row(c.Key("mini-pill")).Fill().Padding(4, 10).Gap(9).Radius(14).
		Background(ui.RGBA(34, 34, 32, 0.94)).Border(1, ColorSurfaceBorder).
		AlignItems(ui.Center).Cursor(ui.CursorPointer)
	if pill.Clicked() && m.OnOpenPanel != nil {
		m.OnOpenPanel()
	}

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
			nowSec := float64(now.UnixNano()) / 1e9
			models := st.SpeedLog.Models(nowSec)
			v := "–"
			if len(models) > 0 {
				if r := core.ComputeSpeedRate(models[0].Requests, nowSec-core.SpeedRecentWindow, nowSec); r != nil {
					v = fmt.Sprintf("%d", int(math.Round(r.TokensPerSecond())))
				}
			}
			addCol(tr(zh, "速度", "tok/s"), v, ColorText)
		}
	})
}
