package appui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"ccmonitor/internal/core"
	"ccmonitor/internal/platform"
	"ccmonitor/internal/service"
)

type PopoverActions struct {
	OpenSettings      func()
	Quit              func()
	ChooseDir         func()
	TogglePill        func()
	OnProviderChanged func()
}

type PopoverModel struct {
	Store   *service.Store
	Actions PopoverActions

	// Ephemeral UI hover & disclosure state (main thread only)
	procExpanded          bool
	providerMenuOpen      bool
	suppressProviderClick bool
	hoveredBar            map[string]bool
	hoveredDay            int
	hoveredAge            float64
	hoveredReply          uint64

	// Antigravity ephemeral UI state
	agProcExpanded  bool
	agHoveredBucket map[string]bool
	agHoveredBar    int
	agHoveredSpark  int
	agShowDotChart  bool
	agHoveredDotID  int
}

func NewPopoverModel(store *service.Store, actions PopoverActions) *PopoverModel {
	return &PopoverModel{
		Store:           store,
		Actions:         actions,
		hoveredBar:      map[string]bool{},
		hoveredDay:      -1,
		hoveredAge:      -1,
		agHoveredBucket: map[string]bool{},
		agHoveredBar:    -1,
		agHoveredSpark:  -1,
	}
}

// ResetHover clears ephemeral hover state when the popover closes or hides.
func (m *PopoverModel) ResetHover() {
	m.providerMenuOpen = false
	m.suppressProviderClick = false
	m.hoveredAge = -1
	m.hoveredReply = 0
	m.hoveredDay = -1
	for k := range m.hoveredBar {
		delete(m.hoveredBar, k)
	}
	m.agHoveredBar = -1
	m.agHoveredSpark = -1
	m.agHoveredDotID = 0
	for k := range m.agHoveredBucket {
		delete(m.agHoveredBucket, k)
	}
}

func (m *PopoverModel) switchProvider(p core.ProviderKind) {
	norm := core.NormalizeProvider(p)
	m.providerMenuOpen = false
	m.suppressProviderClick = false
	cur := core.NormalizeProvider(m.Store.ViewState().Prefs.ActiveProvider)
	if cur == norm {
		return
	}
	m.ResetHover()
	m.Store.SetActiveProvider(norm)
	if m.Actions.OnProviderChanged != nil {
		m.Actions.OnProviderChanged()
	}
}

func (m *PopoverModel) View(c *ui.Context) {
	st := m.Store.ViewState()
	provider := core.NormalizeProvider(st.Prefs.ActiveProvider)
	ApplyThemeFor(c, provider)
	c.Root().Background(ui.Transparent)

	// Shortcuts matching original app: Ctrl+, settings, Ctrl+R refresh, Ctrl+Q quit
	// Plus provider switching: Ctrl+1 Claude, Ctrl+2 Antigravity, Ctrl+Tab toggle.
	c.OnShortcut(ui.Cmd, ui.KeyComma, func() {
		if m.Actions.OpenSettings != nil {
			m.Actions.OpenSettings()
		}
	})
	c.OnShortcut(ui.Cmd, ui.KeyR, func() { m.Store.RefreshNow() })
	c.OnShortcut(ui.Cmd, ui.KeyQ, func() {
		if m.Actions.Quit != nil {
			m.Actions.Quit()
		}
	})
	c.OnShortcut(ui.Cmd, ui.Key1, func() { m.switchProvider(core.ProviderClaude) })
	c.OnShortcut(ui.Cmd, ui.Key2, func() { m.switchProvider(core.ProviderAntigravity) })
	c.OnShortcut(ui.Cmd, ui.KeyTab, func() {
		if provider == core.ProviderAntigravity {
			m.switchProvider(core.ProviderClaude)
		} else {
			m.switchProvider(core.ProviderAntigravity)
		}
	})

	zh := st.Prefs.IsChinese()
	now := time.Now()

	if c.WindowHidden() {
		m.ResetHover()
	}

	// Match claude-usage-monitor: tick every 1s while panel is visible when countdown
	// or output speed is active (shifts timeline, drops expired 5m replies, ticks generating timer).
	if st.Prefs.ShowResetCountdown ||
		(provider == core.ProviderClaude && st.Prefs.SpeedDisplay != core.DisplayHidden) ||
		(provider == core.ProviderAntigravity && st.AG.LocalReport.OutputSpeed.IsRecentlyActive) {
		c.After(time.Second)
	} else {
		c.After(15 * time.Second)
	}

	root := ui.Column(c).Fill().Width(PopoverWidth).Padding(13, PanelPadH, 8, PanelPadH).Gap(0)
	if c.Vibrancy() {
		root.Background(ui.RGBA(20, 20, 18, 0.72))
	} else {
		root.Background(ColorSurfacePanel)
	}

	root.Children(func() {
		// 1. Header: brand logo + dropdown title + PlanBadge (pinned at top)
		if provider == core.ProviderAntigravity {
			m.renderAGHeader(c, st, now, zh)
		} else {
			m.renderHeader(c, st, now, zh)
		}

		// Scrollable middle body: expands to natural height via FitContent when
		// the screen has room, and scrolls smoothly if content exceeds WorkArea.
		ui.Scroll(c.Key("popover-body")).FillWidth().Grow(1).Scrollbars(false).Children(func() {
			// 2. Exit / Network Place row (shared by Claude & Antigravity)
			m.renderExitRow(c, st, zh)

			if provider == core.ProviderAntigravity {
				m.renderAGBody(c, st, now, zh)
				return
			}

			// 3. Running Claude processes (collapsible)
			if st.Prefs.ShowProcesses && len(st.Processes) > 0 {
				m.renderProcesses(c, st, zh)
			}

			Hairline(c)

			// 4. Main content sections (Limits, Speed, Spend)
			m.renderSections(c, st, now, zh)
		})

		Hairline(c)

		// 5. Footer: 设置… | 立即刷新 | 退出 (pinned at bottom)
		m.renderFooter(c, st, zh)
	})
}

func (m *PopoverModel) renderProviderTrigger(c *ui.Context, st service.ViewState, now time.Time, zh bool) {
	active := core.NormalizeProvider(st.Prefs.ActiveProvider)
	trigger := ui.Row(c.Key("provider-trigger")).Height(24).Padding(0, 6).Margin(0, 0, 0, -6).
		Radius(7).Gap(6).AlignItems(ui.Center).Cursor(ui.CursorPointer)
	hov := trigger.Hovered()
	if m.providerMenuOpen {
		trigger.Background(ColorPillSel)
	} else if hov {
		trigger.Background(ColorHover)
	}

	if trigger.Clicked() {
		if m.suppressProviderClick {
			m.suppressProviderClick = false
		} else {
			m.providerMenuOpen = !m.providerMenuOpen
		}
	} else if !trigger.Pressed() {
		m.suppressProviderClick = false
	}

	trigger.Tooltip(tr(zh, "切换用量面板 (Ctrl+1 / Ctrl+2)", "Switch usage panel (Ctrl+1 / Ctrl+2)"))
	trigger.Children(func() {
		if active == core.ProviderAntigravity {
			AntigravityLogo(c, 15, 0)
			ui.Text(c, tr(zh, "Antigravity 用量", "Antigravity usage")).FontSize(13).FontWeight(600).TextColor(ColorText).SingleLine()
		} else {
			ClaudeLogo(c, 15, ColorAccent)
			ui.Text(c, tr(zh, "Claude 用量", "Claude usage")).FontSize(13).FontWeight(600).TextColor(ColorText).SingleLine()
		}
		chevCol := ColorTertiary
		if hov || m.providerMenuOpen {
			chevCol = ColorSecondary
		}
		ui.Icon(c, IconChevDn).FontSize(9.5).TextColor(chevCol)
	})

	pop := ui.PopoverBase(c.Key("provider-pop"), trigger, &m.providerMenuOpen, func(panel ui.Element) {
		panel.Width(212).Margin(4, 0, 0, 0).Padding(4).Radius(10).
			Background(ui.Hex("#2E2E2C")).Border(1, ui.RGBA(255, 255, 255, 0.14))

		claudePct, claudeCol := "–", ColorTertiary
		if st.OfficialUsage != nil {
			eff := st.OfficialUsage.EffectiveAt(now)
			if eff.FiveHour != nil {
				claudePct = fmt.Sprintf("%d%%", eff.FiveHour.Percent())
				claudeCol = LevelColor(eff.FiveHour.Fraction())
			}
		}

		agPct, agCol := "--", ColorTertiary
		if b := st.AG.OfficialReport.PrimaryFiveHour; b != nil {
			agPct = fmt.Sprintf("%d%%", b.UsedPercentInt())
			agCol = AGLevelColor(b.UsedFraction, b.IsOverPace())
		}

		ui.Column(c).FillWidth().Gap(2).Children(func() {
			renderItem := func(key string, p core.ProviderKind, label, pctStr string, pctCol, checkCol ui.Color, drawLogo func()) {
				isSel := active == p
				item := ui.Row(c.Key(key)).FillWidth().Height(30).Padding(0, 8).Radius(7).Gap(8).
					AlignItems(ui.Center).Cursor(ui.CursorPointer)
				if isSel {
					item.Background(ColorPillBg)
				}
				if item.Hovered() {
					item.Background(ColorHover)
				}
				if item.Clicked() {
					m.switchProvider(p)
				}
				item.Children(func() {
					drawLogo()
					txtCol := ColorSecondary
					weight := 400
					if isSel {
						txtCol = ColorText
						weight = 500
					}
					ui.Text(c, label).FontSize(12.5).FontWeight(weight).TextColor(txtCol).SingleLine()
					ui.Spacer(c)
					ui.Text(c, pctStr).FontSize(11.5).FontWeight(500).TextColor(pctCol).FontFeatures("tnum").SingleLine()
					ui.Row(c).Size(14, 14).Center().Children(func() {
						if isSel {
							ui.Icon(c, IconCheck).FontSize(12).TextColor(checkCol)
						}
					})
				})
			}

			renderItem("pdrop-claude", core.ProviderClaude, "Claude", claudePct, claudeCol, ColorAccent, func() {
				ClaudeLogo(c, 13, ColorAccent)
			})
			renderItem("pdrop-ag", core.ProviderAntigravity, "Antigravity", agPct, agCol, ColorAGAccent, func() {
				AntigravityLogo(c, 13, 0)
			})
		})
	})

	if pop.Dismissed() && trigger.Pressed() {
		m.suppressProviderClick = true
	}
}

func (m *PopoverModel) renderHeader(c *ui.Context, st service.ViewState, now time.Time, zh bool) {
	ui.Row(c).FillWidth().Height(24).Margin(0, 0, 10, 0).Gap(8).AlignItems(ui.Center).Children(func() {
		m.renderProviderTrigger(c, st, now, zh)
		ui.Spacer(c)

		if st.DetectedPlan != core.PlanNone {
			badgeRow := ui.Row(c).Gap(7).AlignItems(ui.Center)
			badgeRow.Children(func() {
				PlanTag(c, st.DetectedPlan)
				if lbl, col := planSubLabel(st, zh); lbl != "" {
					ui.Text(c, lbl).FontSize(11).TextColor(col).FontFeatures("tnum").SingleLine()
				}
			})
			if tip := planTooltip(st, zh); tip != "" {
				badgeRow.Tooltip(tip)
			}
		}
	})
}

func planSubLabel(st service.ViewState, zh bool) (string, ui.Color) {
	if st.SubscriptionHealth != "" && st.SubscriptionHealth != core.HealthActive {
		switch st.SubscriptionHealth {
		case core.HealthPaymentFailed:
			return tr(zh, "扣款失败", "Payment failed"), ColorCritical
		case core.HealthActionRequired:
			return tr(zh, "付款待验证", "Confirm payment"), ColorCritical
		case core.HealthInactive:
			return tr(zh, "订阅已结束", "Ended"), ColorCritical
		}
	}
	if st.SubscriptionTerm == nil {
		return "", ColorTertiary
	}
	if st.Prefs.SubscriptionDisplay == core.ShowOnWarn && !st.SubscriptionTerm.IsEnding() {
		return "", ColorTertiary
	}
	col := ColorTertiary
	if st.SubscriptionTerm.IsEnding() {
		col = ColorWarning
	}
	switch st.SubscriptionTerm.DaysLeft {
	case 0:
		return tr(zh, "今天到期", "Ends today"), col
	case 1:
		return tr(zh, "明天到期", "Ends tomorrow"), col
	default:
		return tr(zh, fmt.Sprintf("还剩 %d 天", st.SubscriptionTerm.DaysLeft), fmt.Sprintf("%d days left", st.SubscriptionTerm.DaysLeft)), col
	}
}

func planTooltip(st service.ViewState, zh bool) string {
	var parts []string
	if st.AccountEmail != "" {
		parts = append(parts, tr(zh, "账号: ", "Account: ")+st.AccountEmail)
	}
	parts = append(parts, tr(zh, "凭据来源: .credentials.json", "Credentials: .credentials.json"))
	if st.SubscriptionTerm != nil {
		parts = append(parts, tr(zh, "本期至: ", "Ends: ")+core.FormatDayWithWeekday(st.SubscriptionTerm.End, zh))
	}
	if st.LoginExpiresAt != nil {
		parts = append(parts, tr(zh, "登录有效至: ", "Login until: ")+st.LoginExpiresAt.Local().Format("2006-01-02 15:04"))
	}
	return strings.Join(parts, "\n")
}

func (m *PopoverModel) renderExitRow(c *ui.Context, st service.ViewState, zh bool) {
	hasIssue := st.Shield == platform.ShieldRisk || st.Shield == platform.ShieldSevere
	if st.Prefs.ExitDisplay == core.ShowOnWarn && !hasIssue {
		return
	}

	pal := PaletteFor(st.Prefs.ActiveProvider)
	isAG := core.NormalizeProvider(st.Prefs.ActiveProvider) == core.ProviderAntigravity

	ui.Column(c).FillWidth().Margin(0, 0, 8, 0).Gap(2).Children(func() {
		ui.Row(c).FillWidth().Height(26).Gap(8).AlignItems(ui.Center).Children(func() {
			if st.ExitPlace == nil && !st.ExitFailed {
				// Checking skeleton
				ui.Box(c).Size(18, 13).Radius(3).Background(ui.RGBA(255, 255, 255, 0.10))
				ui.Text(c, tr(zh, "正在确认出口…", "Checking exit…")).FontSize(12).TextColor(ColorTertiary).SingleLine()
				return
			}
			if st.ExitPlace != nil {
				CountryBadge(c, st.ExitPlace.CountryCode)
				ui.Text(c, st.ExitPlace.CountryName(zh)).FontSize(12.5).FontWeight(500).TextColor(ColorText).SingleLine()
				if st.ExitPlace.RestrictsUsage() {
					if isAG {
						Chip(c, tr(zh, "受限区", "Restricted"), ColorCritical).Tooltip(
							tr(zh, "出口在中国大陆、香港或澳门", "Exit is in CN/HK/MO."),
						)
					} else {
						Chip(c, tr(zh, "已暂停", "Paused"), ColorCritical).Tooltip(
							tr(zh, "出口在中国大陆、香港或澳门，暂停向官方查询用量", "Exit is in CN/HK/MO; official usage checks are paused."),
						)
					}
				} else if st.ExitPlace.IsUnitedStates() {
					Chip(c, tr(zh, "美区", "US"), pal.Accent)
				}
			} else {
				ui.Icon(c, IconGlobe).FontSize(14).TextColor(ColorTertiary)
				ui.Text(c, tr(zh, "无法确认出口", "Exit unknown")).FontSize(12.5).FontWeight(500).TextColor(ColorText).SingleLine()
				if !isAG {
					Chip(c, tr(zh, "已暂停", "Paused"), ColorCritical)
				}
			}
			ui.Spacer(c)
			if st.ExitPlace != nil {
				ipBox := ui.Row(c.Key("exit-ip")).Padding(4, 4)
				hov := ipBox.Hovered()
				txt := core.MaskIP(st.ExitPlace.IP)
				col := ColorTertiary
				if hov {
					txt = st.ExitPlace.IP
					col = ColorSecondary
				}
				if st.ExitPlace.RestrictsUsage() {
					col = ColorCritical
				}
				ipBox.Children(func() {
					ui.Text(c, txt).Font("monospace").FontSize(11.5).FontWeight(500).TextColor(col).SingleLine()
				})
			}
		})
		if st.IPv6IsDirect {
			ui.Row(c).Padding(0, 0, 0, 26).Children(func() {
				ui.Text(c, tr(zh, "严重警告：IPv6 直连中国大陆、香港或澳门", "Severe warning: IPv6 connects directly from CN/HK/MO")).
					FontSize(11).FontWeight(600).TextColor(ColorCritical)
			})
		}
	})
}

func (m *PopoverModel) renderProcesses(c *ui.Context, st service.ViewState, zh bool) {
	ui.Column(c).FillWidth().Margin(0, 0, 8, 0).Gap(4).Children(func() {
		SectionHeader(c, "sec-proc", tr(zh, "进程", "Processes"), !m.procExpanded, func() {
			m.procExpanded = !m.procExpanded
		}, func() {
			ui.Text(c, tr(zh, fmt.Sprintf("%d 个", len(st.Processes)), fmt.Sprintf("%d", len(st.Processes)))).
				FontSize(10.5).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
		})
		if m.procExpanded {
			ui.Column(c).FillWidth().Gap(2).Children(func() {
				for _, task := range st.Processes {
					t := task
					row := ui.Row(c.Key(fmt.Sprintf("proc-%d", t.PID))).FillWidth().Height(26).Padding(0, 4).Gap(8).Radius(6).AlignItems(ui.Center)
					hov := row.Hovered()
					if hov {
						row.Background(ColorHover)
					}
					row.Children(func() {
						ui.Text(c, t.Name).FontSize(12.5).FontWeight(500).TextColor(ColorText).SingleLine()
						if t.Detail != "" {
							ui.Text(c, t.Detail).FontSize(11.5).TextColor(ColorTertiary).SingleLine().Grow(1)
						} else {
							ui.Spacer(c)
						}
						if hov {
							fq := ui.Row(c.Key("fq")).Height(18).Padding(0, 8).Radius(9).Center().
								Background(ColorCritical.Alpha(0.16)).Cursor(ui.CursorPointer)
							if fq.Clicked() {
								m.Store.ForceQuitProcess(t.PID)
							}
							fq.Children(func() {
								ui.Text(c, tr(zh, "强制退出", "Force quit")).FontSize(10.5).FontWeight(600).TextColor(ColorCritical).SingleLine()
							})
						} else {
							ui.Text(c, platform.FormatBytes(t.Bytes)).FontSize(11.5).FontWeight(500).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
						}
					})
				}
			})
		}
	})
}

func (m *PopoverModel) renderSections(c *ui.Context, st service.ViewState, now time.Time, zh bool) {
	showLimits := st.Prefs.LimitsDisplay != core.DisplayHidden
	showSpeed := st.Prefs.SpeedDisplay != core.DisplayHidden
	showSpend := st.Prefs.SpendDisplay != core.DisplayHidden

	if !showLimits && !showSpeed && !showSpend {
		ui.Box(c).FillWidth().Padding(16, 0).Children(func() {
			EmptyNote(c, IconSliders, tr(zh, "面板里的内容都已隐藏", "Everything in the panel is hidden"),
				tr(zh, "可以在「设置 › 面板」中选择要显示的内容", "Choose what to show in Settings > Panel."), nil)
		})
		return
	}

	sectionCount := 0
	sep := func() {
		if sectionCount > 0 {
			Hairline(c)
		}
		sectionCount++
	}

	if showLimits {
		sep()
		m.renderLimitsSection(c, st, now, zh)
	}

	if showSpeed || showSpend {
		switch st.Phase {
		case service.PhaseLaunching, service.PhaseIndexing:
			sep()
			m.renderIndexing(c, st, zh)
		case service.PhaseEmpty:
			sep()
			m.renderNoSessions(c, st, zh)
		case service.PhaseReady:
			if showSpeed {
				sep()
				m.renderSpeedSection(c, st, now, zh)
			}
			if showSpend && st.Snapshot != nil {
				sep()
				m.renderSpendSection(c, st, now, zh)
			}
		}
	}
}

func (m *PopoverModel) renderLimitsSection(c *ui.Context, st service.ViewState, now time.Time, zh bool) {
	rows := core.BuildLimitRows(st.OfficialUsage, now, zh)
	collapsed := st.Prefs.CollapsedLimits

	ui.Column(c).FillWidth().Padding(12, 0).Gap(11).Children(func() {
		SectionHeader(c, "sec-limits", tr(zh, "用量限额", "Limits"), collapsed, func() {
			m.Store.UpdatePrefs(func(p *core.Preferences) { p.CollapsedLimits = !p.CollapsedLimits })
		}, func() {
			if collapsed && len(rows) > 0 {
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					for _, row := range rows {
						ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
							ui.Text(c, row.ShortTitle).FontSize(10.5).TextColor(ColorTertiary).SingleLine()
							ui.Text(c, fmt.Sprintf("%d%%", row.Percent)).FontSize(10.5).FontWeight(500).
								TextColor(LevelColor(row.Fraction)).FontFeatures("tnum").SingleLine()
						})
					}
				})
				return
			}
			ui.Text(c, limitsStatusText(st, now, zh)).FontSize(10.5).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
		})

		if collapsed {
			return
		}

		if len(rows) == 0 {
			m.renderLimitsUnavailable(c, st, zh)
			return
		}

		if st.Prefs.LimitsDisplay == core.DisplayCompact {
			ui.Column(c).FillWidth().Gap(8).Children(func() {
				for _, row := range rows {
					line := row.Pace(now)
					col := LevelColor(row.Fraction)
					var mark *float64
					exceeded := false
					if line != nil {
						f := line.Fraction
						mark = &f
						exceeded = line.IsExceeded(row.Fraction)
					}
					ui.Row(c.Key("cl-"+row.ID)).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Text(c, row.ShortTitle).Width(42).FontSize(12.5).TextColor(ColorText).SingleLine()
						ui.Box(c).Grow(1).Children(func() {
							ThinBar(c, "cbar-"+row.ID, row.Fraction, col, mark, exceeded, nil)
						})
						ui.Text(c, fmt.Sprintf("%d%%", row.Percent)).Width(34).TextAlign(ui.End).
							FontSize(12.5).FontWeight(500).TextColor(col).FontFeatures("tnum").SingleLine()
					})
				}
			})
		} else {
			ui.Column(c).FillWidth().Gap(10).Children(func() {
				for _, row := range rows {
					m.renderLimitRowDetailed(c, row, st.Prefs, now, zh)
				}
			})
		}

		if st.OfficialState == service.OfficialNoCredentials || st.OfficialState == service.OfficialExpired || st.OfficialState == service.OfficialSignedOut {
			m.renderLimitsUnavailable(c, st, zh)
		}
	})
}

func (m *PopoverModel) renderLimitRowDetailed(c *ui.Context, row core.LimitRow, prefs core.Preferences, now time.Time, zh bool) {
	col := LevelColor(row.Fraction)
	line := row.Pace(now)
	var mark *float64
	exceeded := false
	if line != nil {
		f := line.Fraction
		mark = &f
		exceeded = line.IsExceeded(row.Fraction)
	}
	pointing := m.hoveredBar[row.ID]

	ui.Column(c.Key("lr-"+row.ID)).FillWidth().Gap(3).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Baseline).Children(func() {
			ui.Text(c, row.Title).FontSize(12.5).TextColor(ColorText).SingleLine()
			ui.Spacer(c)
			ui.Text(c, fmt.Sprintf("%d%%", row.Percent)).FontSize(12.5).FontWeight(500).
				TextColor(col).FontFeatures("tnum").SingleLine()
		})

		ThinBar(c, "bar-"+row.ID, row.Fraction, col, mark, exceeded, &pointing)
		m.hoveredBar[row.ID] = pointing

		if row.ResetKind != core.ResetNone {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				if pointing && line != nil {
					markStr := core.FormatSafePercent(line.Fraction)
					ui.Text(c, tr(zh, "安全线 "+markStr+" · ", "Safe line "+markStr+" · ")).
						FontSize(10.5).TextColor(ColorSecondary).FontFeatures("tnum").SingleLine()
					if line.IsExceeded(row.Fraction) {
						over := core.FormatSafePercent(row.Fraction - line.Fraction)
						ui.Text(c, tr(zh, "超出 "+over, over+" over")).FontSize(10.5).TextColor(ColorCritical).FontFeatures("tnum").SingleLine()
					} else if row.Fraction > line.Fraction {
						ui.Text(c, tr(zh, "窗口刚开始", "Just started")).FontSize(10.5).TextColor(ColorSecondary).SingleLine()
					} else {
						left := core.FormatSafePercent(line.Fraction - row.Fraction)
						ui.Text(c, tr(zh, "还可用 "+left, left+" left")).FontSize(10.5).TextColor(ColorSecondary).FontFeatures("tnum").SingleLine()
					}
				} else {
					leftTxt, rightClk := formatLimitReset(row, prefs, now, zh)
					ui.Text(c, leftTxt).FontSize(10.5).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
					ui.Spacer(c)
					if rightClk != "" {
						ui.Text(c, rightClk).FontSize(10.5).TextColor(ColorSecondary).FontFeatures("tnum").SingleLine()
					}
				}
			})
		}
	})
}

func formatLimitReset(row core.LimitRow, prefs core.Preferences, now time.Time, zh bool) (string, string) {
	switch row.ResetKind {
	case core.ResetElapsed:
		return tr(zh, "已重置", "Reset"), ""
	case core.ResetIdle:
		return tr(zh, "空闲中", "Idle"), ""
	case core.ResetCountdown:
		if row.ResetsAt == nil || !row.ResetsAt.After(now) {
			return tr(zh, "已重置", "Reset"), ""
		}
		rem := row.ResetsAt.Sub(now)
		if !prefs.FullDateReset && !prefs.ShowResetCountdown {
			left := core.FormatCountdown(rem, zh)
			return tr(zh, left+"后重置", "Resets in "+left), ""
		}
		mom := core.FormatResetMoment(*row.ResetsAt, prefs.FullDateReset, now, zh)
		clk := ""
		if prefs.ShowResetCountdown {
			clk = core.FormatClock(rem, zh)
		}
		return tr(zh, mom+" 重置", "Resets "+mom), clk
	case core.ResetWeekday:
		if row.ResetsAt == nil || !row.ResetsAt.After(now) {
			return tr(zh, "已重置", "Reset"), ""
		}
		mom := core.FormatResetMoment(*row.ResetsAt, prefs.FullDateReset, now, zh)
		clk := ""
		if prefs.ShowResetCountdown {
			clk = core.FormatClock(row.ResetsAt.Sub(now), zh)
		}
		return tr(zh, mom+" 重置", "Resets "+mom), clk
	default:
		return "", ""
	}
}

func limitsStatusText(st service.ViewState, now time.Time, zh bool) string {
	if st.IsFetching {
		return tr(zh, "正在同步…", "Syncing…")
	}
	if st.OfficialUsage == nil {
		switch st.OfficialState {
		case service.OfficialDisabled:
			return tr(zh, "已关闭", "Off")
		case service.OfficialNoCredentials:
			return tr(zh, "未登录", "Not signed in")
		case service.OfficialExpired:
			return tr(zh, "登录已过期", "Login expired")
		case service.OfficialSignedOut:
			return tr(zh, "需要重新登录", "Sign in again")
		case service.OfficialFailed:
			if st.IsRateLimited && st.RetryWait > 0 {
				mins := max(1, int(math.Ceil(st.RetryWait.Minutes())))
				return tr(zh, fmt.Sprintf("限流，约 %d 分钟后重试", mins), fmt.Sprintf("Rate limited, ~%dm", mins))
			}
			return st.OfficialFailMsg
		default:
			return tr(zh, "尚未查询", "Not checked yet")
		}
	}
	rel := core.FormatRelative(st.OfficialUsage.FetchedAt, now, zh)
	synced := tr(zh, rel+"同步", "Synced "+rel)
	if st.OfficialState == service.OfficialConnected {
		return synced
	}
	if st.IsRateLimited && st.RetryWait > 0 {
		mins := max(1, int(math.Ceil(st.RetryWait.Minutes())))
		return synced + tr(zh, fmt.Sprintf(" · 限流 %d 分", mins), fmt.Sprintf(" · 429 %dm", mins))
	}
	return synced
}

func (m *PopoverModel) renderLimitsUnavailable(c *ui.Context, st service.ViewState, zh bool) {
	var ic *ui.SVG
	var title, detail string
	showLogin := false

	switch st.OfficialState {
	case service.OfficialDisabled:
		ic = IconGauge
		title = tr(zh, "官方用量已关闭", "Official usage is off")
		detail = tr(zh, "可以在「设置 › 用量」中开启", "Turn it on in Settings > Usage.")
	case service.OfficialNoCredentials:
		ic = IconTerm
		title = tr(zh, "未找到 Claude 凭据文件", "No Claude credentials file found")
		detail = tr(zh, "请通过 Claude Code 登录生成 .credentials.json，或在「设置 › 数据」指定 .claude 目录", "Sign in via Claude Code to create .credentials.json, or set your .claude directory in Settings > Data.")
		showLogin = true
	case service.OfficialExpired:
		ic = IconClock
		title = tr(zh, "登录已过期", "Login has expired")
		detail = tr(zh, "开启自动续期或重新登录 Claude Code", "Enable auto-renewal or sign in again via Claude Code.")
		showLogin = true
	case service.OfficialSignedOut:
		ic = IconTerm
		title = tr(zh, "登录已失效", "Login is no longer valid")
		detail = tr(zh, "重新登录 Claude Code 后自动恢复官方额度显示", "Sign in again via Claude Code to restore official limits.")
		showLogin = true
	case service.OfficialFailed:
		ic = IconWarning
		title = tr(zh, "暂时无法获取官方用量", "Official usage unavailable")
		detail = st.OfficialFailMsg
	default:
		ic = IconGauge
		if st.IsFetching {
			title = tr(zh, "正在获取官方用量…", "Fetching official usage…")
		} else {
			title = tr(zh, "还没有官方数据", "No official numbers yet")
			detail = tr(zh, "点底部「立即刷新」获取最新额度", "Click Refresh below to fetch official limits.")
		}
	}

	var action func()
	if showLogin {
		action = func() {
			ui.Row(c).Gap(6).Margin(4, 0, 0, -6).Children(func() {
				FooterButton(c, "btn-cli-login", IconTerm, tr(zh, "终端登录", "CLI Login"), false, func() {
					m.Store.LaunchClaudeCodeLogin()
				})
				if m.Actions.OpenSettings != nil {
					FooterButton(c, "btn-open-cfg", IconFolder, tr(zh, "配置目录", "Config Dir"), false, func() {
						m.Actions.OpenSettings()
					})
				}
			})
		}
	}
	EmptyNote(c, ic, title, detail, action)
}

func (m *PopoverModel) renderSpeedSection(c *ui.Context, st service.ViewState, now time.Time, zh bool) {
	nowSec := float64(now.Unix()) + float64(now.Nanosecond())/1e9
	models := st.SpeedLog.Models(nowSec)
	cur := st.ActiveSpeedModel(models)
	var reqs []core.TimedRequest
	var pending []core.PendingRequest
	if cur != nil {
		reqs = cur.Requests
		pending = cur.Pending
	}
	recent := core.ComputeSpeedRate(reqs, nowSec-core.SpeedRecentWindow, nowSec)
	hour := core.ComputeSpeedRate(reqs, nowSec-core.SpeedSpanWindow, nowSec)
	replied := false
	for _, mod := range models {
		if len(mod.Requests) > 0 && mod.Requests[len(mod.Requests)-1].End > nowSec-core.SpeedSpanWindow {
			replied = true
			break
		}
	}
	untimed := !replied && st.SpeedLog.LastUntimed > nowSec-core.SpeedSpanWindow
	collapsed := st.Prefs.CollapsedSpeed
	detailed := st.Prefs.SpeedDisplay == core.DisplayDetailed
	if collapsed || !replied {
		m.hoveredAge = -1
		m.hoveredReply = 0
	}

	ui.Column(c).FillWidth().Padding(14, 0).Gap(10).Children(func() {
		SectionHeader(c, "sec-speed", tr(zh, "输出速度", "Output speed"), collapsed, func() {
			m.Store.UpdatePrefs(func(p *core.Preferences) { p.CollapsedSpeed = !p.CollapsedSpeed })
		}, func() {
			if collapsed {
				sumRow := ui.Row(c).Gap(5).AlignItems(ui.Center)
				if tip := speedSummaryTooltip(recent, hour, cur, replied, untimed, zh); tip != "" {
					sumRow.Tooltip(tip)
				}
				sumRow.Children(func() {
					if len(pending) > 0 {
						ui.Box(c).Size(5, 5).Radius(2.5).Background(ColorLive)
					}
					if recent != nil {
						ui.Row(c).AlignItems(ui.Baseline).Gap(0).Children(func() {
							ui.Text(c, fmt.Sprintf("%d", int(math.Round(recent.TokensPerSecond())))).
								FontSize(10.5).FontWeight(500).TextColor(ColorSecondary).FontFeatures("tnum").SingleLine()
							ui.Text(c, " tok/s").FontSize(10.5).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
						})
					} else if len(pending) > 0 {
						ui.Text(c, tr(zh, "生成中", "Generating")).FontSize(10.5).TextColor(ColorTertiary).SingleLine()
					} else if untimed {
						ui.Text(c, tr(zh, "更新 Claude Code 后才能计时", "Update Claude Code to time replies")).FontSize(10.5).TextColor(ColorTertiary).SingleLine()
					} else if replied {
						ui.Text(c, tr(zh, "近 5 分钟没有回复", "No replies in 5 min")).FontSize(10.5).TextColor(ColorTertiary).SingleLine()
					} else {
						ui.Text(c, tr(zh, "最近 1 小时没有回复", "No replies in the last hour")).FontSize(10.5).TextColor(ColorTertiary).SingleLine()
					}
				})
				return
			}
			if len(pending) > 0 {
				el := max(0, int(nowSec-pending[0].Start))
				tstr := tr(zh, fmt.Sprintf("%d 秒", el), fmt.Sprintf("%ds", el))
				if el >= 60 {
					tstr = tr(zh, fmt.Sprintf("%d 分 %02d 秒", el/60, el%60), fmt.Sprintf("%dm %02ds", el/60, el%60))
				}
				label := tr(zh, "生成中 · "+tstr, "Generating · "+tstr)
				if len(pending) > 1 {
					label = tr(zh, fmt.Sprintf("%d 个生成中 · %s", len(pending), tstr), fmt.Sprintf("%d generating · %s", len(pending), tstr))
				}
				ui.Row(c).Gap(5).AlignItems(ui.Center).
					Tooltip(tr(zh, "请求已经发出，回复写完后数字会更新", "The request is out. The number updates when the reply is written.")).
					Children(func() {
						ui.Box(c).Size(5, 5).Radius(2.5).Background(ColorLive)
						ui.Text(c, label).FontSize(10.5).TextColor(ColorSecondary).FontFeatures("tnum").SingleLine()
					})
			}
		})

		if collapsed {
			return
		}

		if !replied {
			if untimed {
				EmptyNote(c, IconClock, tr(zh, "更新 Claude Code 后才能计时", "Update Claude Code to time replies"),
					tr(zh, "2.1.304 起会记下每个请求发出的时刻", "Version 2.1.304 and later record when each request is sent."), nil)
			} else if len(pending) > 0 {
				EmptyNote(c, IconPulse, tr(zh, "回复写完后显示速度", "The speed appears once the reply is written"), "", nil)
			} else {
				EmptyNote(c, IconPulse, tr(zh, "最近 1 小时没有回复", "No replies in the last hour"),
					tr(zh, "Claude Code 写完回复后，这里显示每秒输出多少 token", "Once Claude Code writes a reply, its tokens per second show here."), nil)
			}
			return
		}

		var trend []core.TrendPoint
		if !detailed {
			trend = core.ComputeSpeedTrend(reqs, nowSec-core.SpeedSpanWindow, nowSec)
		}
		ui.Row(c).FillWidth().AlignItems(ui.End).Gap(12).Children(func() {
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				// Headline: "71 tok/s" with recent 5m tooltip (compact also includes 1h summary)
				var tipHour *core.SpeedRate
				if !detailed {
					tipHour = hour
				}
				ui.Row(c).AlignItems(ui.Baseline).Gap(4).
					Tooltip(speedRecentTooltip(recent, tipHour, cur, true, zh)).
					Children(func() {
						valStr := "–"
						valCol := ColorTertiary
						if recent != nil {
							valStr = fmt.Sprintf("%d", int(math.Round(recent.TokensPerSecond())))
							valCol = ColorText
						}
						ui.Text(c, valStr).FontSize(26).FontWeight(600).LetterSpacing(-0.4).TextColor(valCol).FontFeatures("tnum").SingleLine()
						ui.Text(c, "tok/s").FontSize(12).FontWeight(500).TextColor(ColorTertiary).SingleLine()
					})

				// Subtitle: hovered sparkline point OR "近 5 分钟 · Opus 5.5"
				if !detailed && m.hoveredAge >= 0 && len(trend) > 0 {
					if ni := nearestTrendIndex(trend, nowSec-m.hoveredAge); ni >= 0 {
						ui.Text(c, speedMomentText(trend[ni].T, reqs, zh)).
							FontSize(10.5).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
					}
				} else {
					sub := tr(zh, "近 5 分钟", "Last 5 min")
					if cur != nil {
						sub += " · " + cur.Title(zh)
					}
					sw := ui.Row(c.Key("speed-model-sw")).Gap(4).AlignItems(ui.Center)
					if len(models) > 1 {
						sw.Cursor(ui.CursorPointer)
						sw.Tooltip(speedModelsTooltip(models, cur, nowSec, zh))
						if sw.Clicked() {
							m.Store.CycleSpeedModel(models)
						}
					}
					swCol := ColorTertiary
					if len(models) > 1 && sw.Hovered() {
						swCol = ColorSecondary
					}
					sw.Children(func() {
						ui.Text(c, sub).FontSize(10.5).TextColor(swCol).SingleLine()
						if len(models) > 1 {
							ui.Icon(c, IconChevDn).FontSize(8).TextColor(swCol)
						}
					})
				}
			})

			if detailed {
				if hour != nil {
					ui.Column(c).AlignItems(ui.End).Gap(3).
						Tooltip(speedHourTooltip(hour, cur, zh)).
						Children(func() {
							ui.Text(c, tr(zh, fmt.Sprintf("1 小时 %d tok/s", int(math.Round(hour.TokensPerSecond()))),
								fmt.Sprintf("1 hr %d tok/s", int(math.Round(hour.TokensPerSecond()))))).
								FontSize(10.5).TextColor(ColorSecondary).FontFeatures("tnum").SingleLine()
							ui.Text(c, tr(zh, fmt.Sprintf("%s 次 · %s tokens", core.FormatGrouped(int64(hour.Count)), core.FormatMagnitude(int64(hour.Output), zh)),
								fmt.Sprintf("%s replies · %s tokens", core.FormatGrouped(int64(hour.Count)), core.FormatMagnitude(int64(hour.Output), zh)))).
								FontSize(10.5).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
						})
				}
			} else {
				SpeedSparkline(c, "speed-spark", trend, nowSec, &m.hoveredAge)
			}
		})

		if detailed {
			ui.Column(c).FillWidth().Gap(6).Children(func() {
				SpeedChart(c, "speed-chart", reqs, pending, nowSec, &m.hoveredReply)
				// Time axis or hovered reply detail
				var hovReq *core.TimedRequest
				if m.hoveredReply != 0 {
					for i := range reqs {
						if reqs[i].ID == m.hoveredReply {
							hovReq = &reqs[i]
							break
						}
					}
				}
				if hovReq != nil {
					ui.Text(c, speedReplyDetail(*hovReq, zh)).
						FontSize(10.5).TextColor(ColorSecondary).FontFeatures("tnum").SingleLine()
				} else {
					// Three equal flex columns guarantee "30 min ago" sits dead-center at 50% width.
					ui.Row(c).FillWidth().Children(func() {
						ui.Row(c).Grow(1).Justify(ui.Start).Children(func() {
							ui.Text(c, tr(zh, "60 分钟前", "60 min ago")).FontSize(10.5).TextColor(ColorTertiary).SingleLine()
						})
						ui.Row(c).Grow(1).Justify(ui.Center).Children(func() {
							ui.Text(c, tr(zh, "30 分钟前", "30 min ago")).FontSize(10.5).TextColor(ColorTertiary).SingleLine()
						})
						ui.Row(c).Grow(1).Justify(ui.End).Children(func() {
							ui.Text(c, tr(zh, "现在", "Now")).FontSize(10.5).TextColor(ColorTertiary).SingleLine()
						})
					})
				}
			})
		}
	})
}

func speedBasisNote(zh bool) string {
	return tr(zh,
		"速度 = 输出 ÷ 生成耗时，按 token 加权。耗时从请求发出算到回复写完，包含排队和等第一个字，所以短回复显得慢。思考的 token 也算输出。",
		"Speed = output ÷ time, weighted by tokens. Time runs from sending the request to the end of the reply, including the queue and the wait for the first token, so short replies look slow. Thinking counts as output.",
	)
}

func speedBreakdownLines(r core.SpeedRate, zh bool) []string {
	sec := fmt.Sprintf("%.1f", r.Duration)
	return []string{
		tr(zh, fmt.Sprintf("回复: %s 次", core.FormatGrouped(int64(r.Count))), fmt.Sprintf("Replies: %s", core.FormatGrouped(int64(r.Count)))),
		tr(zh, fmt.Sprintf("输出: %s tokens", core.FormatGrouped(int64(r.Output))), fmt.Sprintf("Output: %s tokens", core.FormatGrouped(int64(r.Output)))),
		tr(zh, fmt.Sprintf("生成耗时: %s 秒", sec), fmt.Sprintf("Time: %ss", sec)),
	}
}

func speedRecentTooltip(recent, hour *core.SpeedRate, model *core.ModelSpeed, explains bool, zh bool) string {
	title := tr(zh, "近 5 分钟", "Last 5 min")
	if model != nil {
		title += " · " + model.Title(zh)
	}
	lines := []string{title}
	if recent != nil {
		lines = append(lines, speedBreakdownLines(*recent, zh)...)
	}
	if hour != nil {
		w := int(math.Round(hour.TokensPerSecond()))
		lines = append(lines, tr(zh,
			fmt.Sprintf("最近 1 小时: %d tok/s · %s 次", w, core.FormatGrouped(int64(hour.Count))),
			fmt.Sprintf("Last hour: %d tok/s · %s replies", w, core.FormatGrouped(int64(hour.Count))),
		))
	}
	var notes []string
	if recent == nil {
		notes = append(notes, tr(zh, "这 5 分钟里没有写完的回复。", "No reply finished in these 5 minutes."))
	}
	if explains {
		notes = append(notes, speedBasisNote(zh))
	}
	if len(notes) > 0 {
		sep := ""
		if !zh {
			sep = " "
		}
		lines = append(lines, strings.Join(notes, sep))
	}
	return strings.Join(lines, "\n")
}

func speedHourTooltip(hour *core.SpeedRate, model *core.ModelSpeed, zh bool) string {
	if hour == nil {
		return ""
	}
	title := tr(zh, "最近 1 小时", "Last hour")
	if model != nil {
		title += " · " + model.Title(zh)
	}
	lines := []string{title}
	lines = append(lines, speedBreakdownLines(*hour, zh)...)
	lines = append(lines, speedBasisNote(zh))
	return strings.Join(lines, "\n")
}

func speedModelsTooltip(models []core.ModelSpeed, cur *core.ModelSpeed, nowSec float64, zh bool) string {
	lines := []string{tr(zh,
		fmt.Sprintf("最近 1 小时用了 %d 个模型", len(models)),
		fmt.Sprintf("%d models in the last hour", len(models)),
	)}
	for _, m := range models {
		marker := "○ "
		if cur != nil && m.ID() == cur.ID() {
			marker = "● "
		}
		var val string
		if r := core.ComputeSpeedRate(m.Requests, nowSec-core.SpeedSpanWindow, nowSec); r != nil {
			w := int(math.Round(r.TokensPerSecond()))
			val = tr(zh,
				fmt.Sprintf("%d tok/s · %s 次", w, core.FormatGrouped(int64(r.Count))),
				fmt.Sprintf("%d tok/s · %s replies", w, core.FormatGrouped(int64(r.Count))),
			)
		} else if len(m.Pending) > 0 {
			val = tr(zh, "生成中", "Generating")
		} else {
			val = "–"
		}
		lines = append(lines, marker+m.Title(zh)+": "+val)
	}
	lines = append(lines, tr(zh, "点击换下一个", "Click to show the next one."))
	return strings.Join(lines, "\n")
}

func speedSummaryTooltip(recent, hour *core.SpeedRate, model *core.ModelSpeed, replied, untimed bool, zh bool) string {
	if untimed {
		return tr(zh,
			"2.1.304 起的 Claude Code 会记下每个请求发出的时刻，更新后就能计时",
			"Claude Code 2.1.304 and later record when each request is sent. Update to time replies.",
		)
	}
	if !replied {
		return ""
	}
	return speedRecentTooltip(recent, hour, model, false, zh)
}

func speedMomentText(t float64, reqs []core.TimedRequest, zh bool) string {
	tStr := time.Unix(int64(t), 0).Local().Format("15:04")
	r := core.ComputeSpeedRate(reqs, t-core.SpeedRecentWindow, t)
	if r == nil {
		return tr(zh, tStr+" 前 5 分钟没有回复", "No replies in the 5 min to "+tStr)
	}
	w := int(math.Round(r.TokensPerSecond()))
	return tr(zh,
		fmt.Sprintf("%s 前 5 分钟 · %d tok/s", tStr, w),
		fmt.Sprintf("5 min to %s · %d tok/s", tStr, w),
	)
}

// speedReplyDetail formats the hovered scatter point equation using the displayed
// 1-decimal seconds so mental math matches the shown rate (matching SpeedSection.swift).
func speedReplyDetail(r core.TimedRequest, zh bool) string {
	tStr := time.Unix(int64(r.End), 0).Local().Format("15:04:05")
	secStr := fmt.Sprintf("%.1f", r.Duration())
	shown, err := strconv.ParseFloat(secStr, 64)
	if err != nil {
		shown = r.Duration()
	}
	rateStr := "–"
	if shown > 0 {
		rateStr = fmt.Sprintf("%d", int(math.Round(float64(r.Output)/shown)))
	}
	return tr(zh,
		fmt.Sprintf("%s · %s tokens ÷ %s 秒 = %s tok/s", tStr, core.FormatGrouped(int64(r.Output)), secStr, rateStr),
		fmt.Sprintf("%s · %s tokens ÷ %ss = %s tok/s", tStr, core.FormatGrouped(int64(r.Output)), secStr, rateStr),
	)
}

func (m *PopoverModel) renderSpendSection(c *ui.Context, st service.ViewState, now time.Time, zh bool) {
	snap := st.Snapshot
	money := core.NewMoneyFormat(st.Prefs.Currency)
	span := st.Prefs.CostSpan
	collapsed := st.Prefs.CollapsedSpend

	var period *core.PeriodUsage
	switch span {
	case core.SpanMonth:
		period = &snap.Billing
	case core.SpanWeek:
		period = snap.Week
	case core.SpanDay:
		period = &snap.Day
	}

	ui.Column(c).FillWidth().Padding(14, 0).Gap(10).Children(func() {
		SectionHeader(c, "sec-spend", tr(zh, "本期消耗", "Spend"), collapsed, func() {
			m.Store.UpdatePrefs(func(p *core.Preferences) { p.CollapsedSpend = !p.CollapsedSpend })
		}, func() {
			if collapsed {
				lbl := spanShortTitle(span, zh)
				val := tr(zh, "等待同步", "waiting")
				if period != nil {
					val = money.Format(period.Cost)
				}
				ui.Row(c).Gap(4).Children(func() {
					ui.Text(c, lbl).FontSize(10.5).TextColor(ColorTertiary).SingleLine()
					ui.Text(c, val).FontSize(10.5).FontWeight(500).TextColor(ColorSecondary).FontFeatures("tnum").SingleLine()
				})
				return
			}
			if period != nil {
				ui.Text(c, core.FormatPeriodRange(period.Start, period.End, zh)).
					FontSize(10.5).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
			}
		})

		if collapsed {
			return
		}

		// Segmented tabs: 本月周期 | 本周期 | 每日
		selIdx := 1
		switch span {
		case core.SpanMonth:
			selIdx = 0
		case core.SpanWeek:
			selIdx = 1
		case core.SpanDay:
			selIdx = 2
		}
		SegmentedTabs(c, "spend-tabs", []string{
			tr(zh, "本月周期", "Billing"),
			tr(zh, "本周期", "This week"),
			tr(zh, "每日", "Today"),
		}, selIdx, true, func(idx int) {
			m.Store.UpdatePrefs(func(p *core.Preferences) {
				p.CostSpan = core.AllCostSpans[idx]
			})
		})

		if period == nil {
			EmptyNote(c, IconCal, tr(zh, "本周要等每周限额同步", "This week waits for weekly limit"),
				tr(zh, "本周按官方每周额度的重置时间统计，同步一次后显示", "Follows the official weekly reset and appears after one sync."), nil)
			return
		}

		// Header: big amount + projected + subtitle + 14-day MiniBars
		m.renderSpendHeader(c, snap, period, span, money, now, zh)

		if st.Prefs.SpendDisplay == core.DisplayCompact {
			return
		}

		// Stats row: 总 Tokens | 请求 | 今日
		ui.Row(c).FillWidth().AlignItems(ui.Baseline).Children(func() {
			ui.Row(c).Gap(5).AlignItems(ui.Baseline).Children(func() {
				ui.Text(c, tr(zh, "总 Tokens", "Tokens")).FontSize(10.5).TextColor(ColorTertiary).SingleLine()
				ui.Text(c, core.FormatMagnitude(period.Tokens.Total(), zh)).FontSize(12.5).FontWeight(500).TextColor(ColorText).FontFeatures("tnum").SingleLine()
			}).Tooltip(fmt.Sprintf("%s tokens", core.FormatGrouped(period.Tokens.Total())))
			ui.Spacer(c)
			ui.Row(c).Gap(5).AlignItems(ui.Baseline).Children(func() {
				ui.Text(c, tr(zh, "请求", "Requests")).FontSize(10.5).TextColor(ColorTertiary).SingleLine()
				ui.Text(c, tr(zh, core.FormatGrouped(int64(period.Requests))+" 次", core.FormatGrouped(int64(period.Requests)))).
					FontSize(12.5).FontWeight(500).TextColor(ColorText).FontFeatures("tnum").SingleLine()
			})
			ui.Spacer(c)
			todayCost := 0.0
			if td := snap.Today(); td != nil {
				todayCost = td.Cost
			}
			ui.Row(c).Gap(5).AlignItems(ui.Baseline).Children(func() {
				ui.Text(c, tr(zh, "今日", "Today")).FontSize(10.5).TextColor(ColorTertiary).SingleLine()
				ui.Text(c, money.Whole(todayCost)).FontSize(12.5).FontWeight(500).TextColor(ColorText).FontFeatures("tnum").SingleLine()
			})
		})

		// 4-column token breakdown with colored dots (hover shows dollar cost)
		ui.Row(c).FillWidth().AlignItems(ui.Start).Children(func() {
			renderTokenCol(c, "tk-in", tr(zh, "新增输入", "Input"), ColorInputTok, period.Tokens.Input, period.Categories.Input, money, zh)
			renderTokenCol(c, "tk-out", tr(zh, "输出", "Output"), ColorOutputTok, period.Tokens.Output, period.Categories.Output, money, zh)
			renderTokenCol(c, "tk-cw", tr(zh, "缓存创建", "Cache write"), ColorCacheWrite, period.Tokens.CacheWrite(), period.Categories.CacheWrite, money, zh)
			renderTokenCol(c, "tk-cr", tr(zh, "缓存命中", "Cache read"), ColorCacheRead, period.Tokens.CacheRead, period.Categories.CacheRead, money, zh)
		})

		// Cache hit rate + ThinBar
		hitRate := period.Tokens.CacheHitRate()
		ui.Column(c).FillWidth().Gap(3).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Baseline).Gap(6).Children(func() {
				ui.Text(c, tr(zh, "缓存命中率", "Cache hit rate")).FontSize(12.5).TextColor(ColorText).SingleLine()
				ui.Spacer(c)
				ui.Text(c, tr(zh, "省 "+money.Compact(period.Categories.Savings), "Saved "+money.Compact(period.Categories.Savings))).
					FontSize(10.5).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
				ui.Text(c, core.FormatPercent(hitRate, 1)).FontSize(12.5).FontWeight(500).TextColor(ColorText).FontFeatures("tnum").SingleLine()
			})
			ThinBar(c, "cache-hit-bar", hitRate, ui.RGBA(255, 255, 255, 0.42), nil, false, nil)
		})

		// Model breakdown (top 2 + Other)
		if len(period.Models) > 0 {
			m.renderModelList(c, period, money, zh)
		}
	})
}

func (m *PopoverModel) renderSpendHeader(c *ui.Context, snap *core.UsageSnapshot, p *core.PeriodUsage, span core.CostSpan, money core.MoneyFormat, now time.Time, zh bool) {
	recent := snap.Daily
	if len(recent) > 14 {
		recent = recent[len(recent)-14:]
	}
	vals := make([]float64, len(recent))
	periodStart := -1
	for i, d := range recent {
		vals[i] = d.Cost
		if periodStart < 0 && d.Date.Add(24*time.Hour).After(p.Start) {
			periodStart = i
		}
	}

	proj := p.Projection(now, snap.WeeklyAllowance)

	ui.Row(c).FillWidth().AlignItems(ui.End).Gap(8).Children(func() {
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			// Clickable amount cycles currency just like original!
			amtRow := ui.Row(c.Key("spend-amt")).AlignItems(ui.Baseline).Gap(6).Cursor(ui.CursorPointer)
			if amtRow.Clicked() {
				m.Store.UpdatePrefs(func(pr *core.Preferences) {
					idx := 0
					for i, cur := range core.AllCurrencies {
						if cur == pr.Currency {
							idx = i
							break
						}
					}
					pr.Currency = core.AllCurrencies[(idx+1)%len(core.AllCurrencies)]
				})
			}
			amtRow.Tooltip(tr(zh, "点击金额切换货币", "Click amount to switch currency"))
			amtRow.Children(func() {
				ui.Text(c, money.Format(p.Cost)).FontSize(26).FontWeight(600).LetterSpacing(-0.4).TextColor(ColorText).FontFeatures("tnum").SingleLine()
				if proj != nil && (p.DayCount > 1 || p.Cost > 0) {
					ui.Text(c, tr(zh, "预计 "+money.Whole(proj.Cost), "→ "+money.Whole(proj.Cost))).
						FontSize(12).FontWeight(500).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
				}
			})

			// Subtitle: hovered bar day OR "第 1 / 7 天 · 还剩 $2,765"
			if m.hoveredDay >= 0 && m.hoveredDay < len(recent) {
				d := recent[m.hoveredDay]
				ui.Text(c, tr(zh,
					fmt.Sprintf("%s · %s · %s 次", core.FormatDayWithWeekday(d.Date, zh), money.Format(d.Cost), core.FormatGrouped(int64(d.Requests))),
					fmt.Sprintf("%s · %s · %s", core.FormatDayWithWeekday(d.Date, zh), money.Format(d.Cost), core.FormatGrouped(int64(d.Requests))),
				)).FontSize(10.5).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
			} else {
				var sub string
				if span == core.SpanDay {
					sub = tr(zh, "当天", "Today")
				} else {
					dayPart := tr(zh, fmt.Sprintf("第 %d / %d 天", p.DayIndex, p.DayCount), fmt.Sprintf("Day %d of %d", p.DayIndex, p.DayCount))
					if proj != nil {
						left := max(0.0, proj.Cost-p.Cost)
						if left < 0.005 {
							sub = dayPart + " · " + tr(zh, "额度已用满", "limit used up")
						} else {
							sub = dayPart + " · " + tr(zh, "还剩 "+money.Whole(left), money.Whole(left)+" left")
						}
					} else {
						daily := money.Whole(p.DailyAverage(now))
						sub = dayPart + " · " + tr(zh, "日均 "+daily, daily+"/day")
					}
				}
				ui.Text(c, sub).FontSize(10.5).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
			}
		})

		MiniBars(c, "spend-bars", vals, len(recent)-1, periodStart, &m.hoveredDay)
	})
}

func renderTokenCol(c *ui.Context, key, label string, dotColor ui.Color, tokens int64, costUSD float64, money core.MoneyFormat, zh bool) {
	col := ui.Column(c.Key(key)).Grow(1).Gap(4)
	hov := col.Hovered()
	col.Tooltip(fmt.Sprintf("%s: %s tokens (%s)", label, core.FormatGrouped(tokens), money.Format(costUSD)))
	col.Children(func() {
		ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(5, 5).Radius(2.5).Background(dotColor)
			ui.Text(c, label).FontSize(10.5).TextColor(ColorTertiary).SingleLine()
		})
		val := core.FormatMagnitude(tokens, zh)
		if hov {
			val = money.Whole(costUSD)
		}
		ui.Text(c, val).FontSize(13).FontWeight(500).TextColor(ColorText).FontFeatures("tnum").SingleLine()
	})
}

func (m *PopoverModel) renderModelList(c *ui.Context, p *core.PeriodUsage, money core.MoneyFormat, zh bool) {
	type group struct {
		name      string
		cost      float64
		estimated bool
	}
	keep := len(p.Models)
	if keep > 3 {
		keep = 2
	}
	var groups []group
	for i := 0; i < keep; i++ {
		groups = append(groups, group{name: p.Models[i].DisplayName, cost: p.Models[i].Cost, estimated: p.Models[i].IsEstimated})
	}
	if len(p.Models) > keep {
		otherCost := 0.0
		for i := keep; i < len(p.Models); i++ {
			otherCost += p.Models[i].Cost
		}
		if otherCost > 0.005 {
			groups = append(groups, group{name: tr(zh, "其他", "Other"), cost: otherCost})
		}
	}
	costs := make([]float64, len(groups))
	for i, g := range groups {
		costs[i] = g.cost
	}
	shares := core.FormatShares(costs)
	amounts := money.Split(p.Cost, costs)

	ui.Column(c).FillWidth().Gap(5).Margin(2, 0, 0, 0).Children(func() {
		for i, g := range groups {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, g.name).FontSize(11.5).TextColor(ColorSecondary).SingleLine()
				if g.estimated {
					ui.Text(c, tr(zh, "估价", "est.")).FontSize(10.5).TextColor(ColorQuaternary).SingleLine()
				}
				ui.Spacer(c)
				ui.Text(c, shares[i]).Width(44).TextAlign(ui.End).FontSize(11.5).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
				ui.Text(c, amounts[i]).MinWidth(64).TextAlign(ui.End).FontSize(11.5).TextColor(ColorText).FontFeatures("tnum").SingleLine()
			})
		}
	})
}

func (m *PopoverModel) renderIndexing(c *ui.Context, st service.ViewState, zh bool) {
	ui.Column(c).FillWidth().Padding(24, 0).Center().Gap(10).Children(func() {
		pose := "idle"
		tick := (time.Now().UnixMilli() / 350) % 4
		if tick == 1 {
			pose = "right"
		} else if tick == 3 {
			pose = "left"
		}
		c.After(350 * time.Millisecond)
		ClawdMascot(c, 42, ColorAccent, pose, platform.ShieldNone)
		ui.Text(c, tr(zh, "正在建立本地索引", "Building local index")).FontSize(12.5).FontWeight(500).TextColor(ColorText)
		ui.Text(c, tr(zh, "首次需要扫描全部会话日志，之后只做增量更新", "First scan reads all session logs; later updates are incremental.")).
			FontSize(10.5).TextColor(ColorTertiary).TextAlign(ui.Center)
		ui.Box(c).Width(180).Children(func() {
			ThinBar(c, "idx-prog", max(0.02, st.IndexProgress), ColorAccent, nil, false, nil)
		})
		ui.Text(c, core.FormatPercent(st.IndexProgress, 0)).FontSize(10.5).TextColor(ColorTertiary).FontFeatures("tnum")
	})
}

func (m *PopoverModel) renderNoSessions(c *ui.Context, st service.ViewState, zh bool) {
	ui.Column(c).FillWidth().Padding(22, 0).Center().Gap(10).Children(func() {
		ClawdMascot(c, 42, ColorQuaternary, "idle", platform.ShieldNone)
		ui.Text(c, tr(zh, "没有找到 Claude Code 会话记录", "No Claude Code sessions found")).FontSize(12.5).FontWeight(500).TextColor(ColorText)
		rootDesc := strings.Join(st.DataRoots, "\n")
		ui.Text(c, rootDesc).FontSize(10.5).TextColor(ColorTertiary).TextAlign(ui.Center)
		if m.Actions.ChooseDir != nil {
			FooterButton(c, "btn-choose-dir", IconFolder, tr(zh, "选择数据目录…", "Choose data folder…"), false, m.Actions.ChooseDir)
		}
	})
}

func (m *PopoverModel) renderFooter(c *ui.Context, st service.ViewState, zh bool) {
	spinning := st.IsFetching
	if core.NormalizeProvider(st.Prefs.ActiveProvider) == core.ProviderAntigravity {
		spinning = st.AG.IsSyncing || st.AG.IsRefreshing
	}
	ui.Row(c).FillWidth().Padding(7, 0, 0, 0).Margin(0, -8, 0, -8).AlignItems(ui.Center).Children(func() {
		FooterButton(c, "ft-settings", IconSliders, tr(zh, "设置…", "Settings…"), false, func() {
			if m.Actions.OpenSettings != nil {
				m.Actions.OpenSettings()
			}
		}).Tooltip(tr(zh, "快捷键 Ctrl+,", "Shortcut Ctrl+,"))
		ui.Spacer(c)
		FooterButton(c, "ft-refresh", IconRefresh, tr(zh, "立即刷新", "Refresh"), spinning, func() {
			m.Store.RefreshNow()
		}).Tooltip(tr(zh, "快捷键 Ctrl+R", "Shortcut Ctrl+R"))
		ui.Spacer(c)
		FooterButton(c, "ft-quit", IconPower, tr(zh, "退出", "Quit"), false, func() {
			if m.Actions.Quit != nil {
				m.Actions.Quit()
			}
		}).Tooltip(tr(zh, "快捷键 Ctrl+Q", "Shortcut Ctrl+Q"))
	})
}

func spanShortTitle(s core.CostSpan, zh bool) string {
	switch s {
	case core.SpanMonth:
		return tr(zh, "本月", "Month")
	case core.SpanWeek:
		return tr(zh, "本周", "Week")
	default:
		return tr(zh, "今日", "Today")
	}
}

func tr(zh bool, cn, en string) string {
	if zh {
		return cn
	}
	return en
}
