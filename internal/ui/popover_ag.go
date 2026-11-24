package appui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"ccmonitor/internal/core"
	"ccmonitor/internal/platform"
	"ccmonitor/internal/service"
)

func (m *PopoverModel) renderAGHeader(c *ui.Context, st service.ViewState, now time.Time, zh bool) {
	off := st.AG.OfficialReport
	local := st.AG.LocalReport
	ui.Row(c).FillWidth().Height(24).Margin(0, 0, 10, 0).Gap(8).AlignItems(ui.Center).Children(func() {
		m.renderProviderTrigger(c, st, now, zh)
		ui.Spacer(c)

		if off.PlanTier != "" {
			badgeRow := ui.Row(c).Gap(7).AlignItems(ui.Center)
			badgeRow.Children(func() {
				AGPlanTag(c, off.PlanTier)
				if st.Prefs.AGBillingAnchorDay > 0 && local.MonthPeriod != nil && local.MonthPeriod.RemainingDays <= 5 {
					days := local.MonthPeriod.RemainingDays
					col := ColorSecondary
					if days <= 3 {
						col = ColorWarning
					}
					ui.Text(c, tr(zh, fmt.Sprintf("还剩 %d 天", days), fmt.Sprintf("%dd left", days))).
						FontSize(11).TextColor(col).FontFeatures("tnum").SingleLine()
				}
			})
			if tip := agPlanTooltip(st, zh); tip != "" {
				badgeRow.Tooltip(tip)
			}
		}
	})
}

func agPlanTooltip(st service.ViewState, zh bool) string {
	var parts []string
	off := st.AG.OfficialReport
	if off.PlanTier != "" {
		parts = append(parts, tr(zh, "订阅方案: ", "Plan: ")+off.PlanTier)
	}
	if off.Email != "" {
		parts = append(parts, tr(zh, "账号: ", "Account: ")+off.Email)
	}
	if st.Prefs.AGBillingAnchorDay > 0 && st.AG.LocalReport.MonthPeriod != nil {
		parts = append(parts, tr(zh, fmt.Sprintf("账单日: 每月 %d 日", st.Prefs.AGBillingAnchorDay), fmt.Sprintf("Billing day: Day %d", st.Prefs.AGBillingAnchorDay)))
		parts = append(parts, tr(zh, fmt.Sprintf("本周期剩余: %d 天", st.AG.LocalReport.MonthPeriod.RemainingDays), fmt.Sprintf("Period left: %d days", st.AG.LocalReport.MonthPeriod.RemainingDays)))
	}
	return strings.Join(parts, "\n")
}

func (m *PopoverModel) renderAGBody(c *ui.Context, st service.ViewState, now time.Time, zh bool) {
	if st.Prefs.AGProcessesDisplay != core.DisplayHidden {
		m.renderAGProcesses(c, st, zh)
	}
	Hairline(c)

	showLimits := st.Prefs.LimitsDisplay != core.DisplayHidden
	showSpeed := st.Prefs.AGShowEstimatedSpeed && st.Prefs.SpeedDisplay != core.DisplayHidden
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
		m.renderAGLimitsSection(c, st, now, zh)
	}
	if showSpeed {
		sep()
		m.renderAGSpeedSection(c, st, now, zh)
	}
	if showSpend {
		sep()
		m.renderAGSpendSection(c, st, now, zh)
	}
}

func (m *PopoverModel) renderAGProcesses(c *ui.Context, st service.ViewState, zh bool) {
	procs := st.AG.Processes
	expanded := m.agProcExpanded || st.Prefs.AGProcessesDisplay == core.DisplayDetailed
	ui.Column(c).FillWidth().Margin(0, 0, 8, 0).Gap(4).Children(func() {
		SectionHeader(c, "ag-sec-proc", tr(zh, "活跃进程", "Processes"), !expanded, func() {
			m.agProcExpanded = !m.agProcExpanded
		}, func() {
			ui.Text(c, fmt.Sprintf("%d", len(procs))).FontSize(10.5).TextColor(ColorSecondary).FontFeatures("tnum").SingleLine()
		})
		if expanded {
			if len(procs) == 0 {
				ui.Text(c, tr(zh, "当前无运行中的 agy / Antigravity 进程", "No running agy / Antigravity processes")).
					FontSize(10.5).TextColor(ColorTertiary)
				return
			}
			ui.Column(c).FillWidth().Gap(2).Children(func() {
				for _, task := range procs {
					t := task
					ui.Row(c.Key(fmt.Sprintf("ag-proc-%d-%s", t.PID, t.Name))).FillWidth().Height(22).Padding(0, 2).Gap(7).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Size(5, 5).Radius(2.5).Background(ColorLive)
						ui.Text(c, t.Name).FontSize(11.5).FontWeight(500).TextColor(ColorText).SingleLine()
						if t.Detail != "" {
							ui.Text(c, t.Detail).FontSize(10.5).TextColor(ColorTertiary).SingleLine()
						}
						ui.Spacer(c)
						if t.Bytes > 0 {
							ui.Text(c, platform.FormatBytes(t.Bytes)).FontSize(10.5).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
						}
					})
				}
			})
		}
	})
}

func (m *PopoverModel) renderAGLimitsSection(c *ui.Context, st service.ViewState, now time.Time, zh bool) {
	if m.agHoveredBucket == nil {
		m.agHoveredBucket = map[string]bool{}
	}
	off := st.AG.OfficialReport
	collapsed := st.Prefs.AGCollapsedLimits

	ui.Column(c).FillWidth().Padding(11, 0).Gap(10).Children(func() {
		SectionHeader(c, "ag-sec-limits", tr(zh, "用量限额", "Limits"), collapsed, func() {
			m.Store.UpdatePrefs(func(p *core.Preferences) { p.AGCollapsedLimits = !p.AGCollapsedLimits })
		}, func() {
			if collapsed && len(off.Buckets) > 0 {
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					lim := min(2, len(off.Buckets))
					for i := 0; i < lim; i++ {
						b := off.Buckets[i]
						ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
							ui.Text(c, core.AGBucketShortName(b.BucketID, b.WindowKind, zh)).FontSize(10.5).TextColor(ColorTertiary).SingleLine()
							ui.Text(c, fmt.Sprintf("%d%%", b.UsedPercentInt())).FontSize(10.5).FontWeight(500).
								TextColor(AGLevelColor(b.UsedFraction, b.IsOverPace())).FontFeatures("tnum").SingleLine()
						})
					}
				})
				return
			}
			if st.AG.IsSyncing {
				ui.Text(c, tr(zh, "正在同步…", "Syncing…")).FontSize(10.5).TextColor(ColorAGAccent).SingleLine()
			} else if off.HasSnapshot() && off.SnapshotCapturedAt != nil {
				rel := core.FormatRelative(*off.SnapshotCapturedAt, now, zh)
				lbl := tr(zh, rel+"同步", "Synced "+rel)
				col := ColorTertiary
				if off.IsStale {
					lbl += tr(zh, " · 建议刷新", " · Stale")
					col = ColorWarning
				}
				ui.Text(c, lbl).FontSize(10.5).TextColor(col).FontFeatures("tnum").SingleLine()
			}
		})

		if collapsed {
			return
		}

		needsLogin := st.AG.AuthRequired || off.AgentState == "authenticating" || (off.HasSnapshot() && strings.TrimSpace(off.Email) == "")

		if !off.HasSnapshot() || len(off.Buckets) == 0 {
			title := tr(zh, "需要登录 agy CLI", "agy CLI sign-in required")
			if st.AG.IsSyncing {
				title = tr(zh, "正在后台同步官方额度…", "Syncing official limits in background…")
			} else if st.AG.CLIBinaryPath == "" {
				title = tr(zh, "未检测到 agy CLI", "agy CLI not detected")
			}
			detail := tr(zh, "在终端完成一次 agy 登录后即可关闭终端，之后自动后台同步官方额度；本地日志统计可直接使用。",
				"Sign in once with agy in terminal, then close it — official limits sync automatically. Local log stats work without it.")
			EmptyNote(c, IconTerm, title, detail, func() {
				ui.Row(c).Gap(6).Margin(4, 0, 0, 0).Children(func() {
					FooterButton(c, "ag-btn-login", IconTerm, tr(zh, "终端登录 agy", "Sign in agy CLI"), false, func() {
						m.Store.LaunchAgyLogin()
					})
					if st.AG.CLIBinaryPath != "" {
						FooterButton(c, "ag-btn-sync", IconRefresh, tr(zh, "立即同步", "Sync now"), st.AG.IsSyncing, func() {
							go m.Store.SyncAntigravityOfficial(true)
						})
					} else if m.Actions.OpenSettings != nil {
						FooterButton(c, "ag-btn-cfg", IconSliders, tr(zh, "配置路径", "Set path"), false, m.Actions.OpenSettings)
					}
				})
			})
			return
		}

		if st.Prefs.LimitsDisplay == core.DisplayCompact {
			ui.Column(c).FillWidth().Gap(8).Children(func() {
				for _, b := range off.Buckets {
					var mark *float64
					if b.SafeLineFraction != nil && !b.IsResetElapsed {
						v := *b.SafeLineFraction
						mark = &v
					}
					valCol := AGLevelColor(b.UsedFraction, b.IsOverPace())
					if b.IsResetElapsed {
						valCol = ColorAGSafeGreen
					}
					ui.Row(c.Key("ag-cl-"+b.BucketID)).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Text(c, core.AGBucketShortName(b.BucketID, b.WindowKind, zh)).Width(68).FontSize(12).TextColor(ColorText).SingleLine()
						ui.Box(c).Grow(1).Children(func() {
							ThinBar(c, "ag-cbar-"+b.BucketID, b.UsedFraction, AGLevelColor(b.UsedFraction, false), mark, b.IsOverPace(), nil)
						})
						ui.Text(c, fmt.Sprintf("%d%%", b.UsedPercentInt())).Width(34).TextAlign(ui.End).
							FontSize(12).FontWeight(500).TextColor(valCol).FontFeatures("tnum").SingleLine()
					})
				}
			})
		} else {
			ui.Column(c).FillWidth().Gap(9).Children(func() {
				for _, b := range off.Buckets {
					m.renderAGLimitRowDetailed(c, b, st.Prefs, zh)
				}
			})
		}

		if needsLogin {
			EmptyNote(c, IconTerm, tr(zh, "agy CLI 登录已失效", "agy CLI sign-in required"),
				tr(zh, "登录到期或已退出，重新登录后即可关闭终端", "Session expired or signed out. Sign in once, then close terminal."), func() {
					FooterButton(c, "ag-btn-relogin", IconTerm, tr(zh, "终端登录 agy", "Sign in agy CLI"), false, func() {
						m.Store.LaunchAgyLogin()
					})
				})
		}
	})
}

func (m *PopoverModel) renderAGLimitRowDetailed(c *ui.Context, b core.AGQuotaBucketEval, prefs core.Preferences, zh bool) {
	valCol := AGLevelColor(b.UsedFraction, b.IsOverPace())
	if b.IsResetElapsed {
		valCol = ColorAGSafeGreen
	}
	var mark *float64
	if b.SafeLineFraction != nil && !b.IsResetElapsed {
		v := *b.SafeLineFraction
		mark = &v
	}
	pointing := m.agHoveredBucket[b.BucketID]
	title := core.AGBucketDisplayName(b.BucketID, b.WindowKind, zh)

	ui.Column(c.Key("ag-lr-"+b.BucketID)).FillWidth().Gap(3).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Baseline).Children(func() {
			ui.Text(c, title).FontSize(12.5).TextColor(ColorText).SingleLine()
			ui.Spacer(c)
			ui.Text(c, fmt.Sprintf("%d%%", b.UsedPercentInt())).FontSize(12.5).FontWeight(500).
				TextColor(valCol).FontFeatures("tnum").SingleLine()
		})

		ThinBar(c, "ag-bar-"+b.BucketID, b.UsedFraction, AGLevelColor(b.UsedFraction, false), mark, b.IsOverPace(), &pointing)
		m.agHoveredBucket[b.BucketID] = pointing

		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			if pointing && mark != nil {
				markStr := core.FormatSafePercent(*mark)
				if b.IsOverPace() {
					ui.Text(c, tr(zh, "安全线 "+markStr+" · 已超速", "Safe line "+markStr+" · Over pace")).
						FontSize(10.5).TextColor(ColorCritical).FontFeatures("tnum").SingleLine()
				} else {
					headroom := max(0.0, *mark-b.UsedFraction)
					ui.Text(c, tr(zh, "安全线 "+markStr+" · 安全余量 "+core.FormatSafePercent(headroom),
						"Safe line "+markStr+" · Headroom "+core.FormatSafePercent(headroom))).
						FontSize(10.5).TextColor(ColorSecondary).FontFeatures("tnum").SingleLine()
				}
			} else if b.IsResetElapsed {
				ui.Text(c, tr(zh, "已重置", "Reset")).FontSize(10.5).TextColor(ColorTertiary).SingleLine()
			} else if b.RemainingUntilReset != nil {
				rem := *b.RemainingUntilReset
				left := core.FormatCountdown(rem, zh)
				ui.Text(c, tr(zh, "重置倒计时 "+left, "Resets in "+left)).FontSize(10.5).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
				if prefs.ShowResetCountdown {
					ui.Spacer(c)
					ui.Text(c, core.FormatClock(rem, zh)).FontSize(10.5).TextColor(ColorSecondary).FontFeatures("tnum").SingleLine()
				}
			}
		})
	})
}

func (m *PopoverModel) renderAGSpeedSection(c *ui.Context, st service.ViewState, now time.Time, zh bool) {
	speed := st.AG.LocalReport.OutputSpeed
	ctxWin := st.AG.OfficialReport.ContextWindow
	usedPct := 0.0
	var usedTokens, ctxSize int64
	if ctxWin != nil {
		if ctxWin.UsedPercentage != nil {
			usedPct = max(0, min(100, *ctxWin.UsedPercentage))
		}
		if ctxWin.TotalInputTokens != nil {
			usedTokens += *ctxWin.TotalInputTokens
		}
		if ctxWin.TotalOutputTokens != nil {
			usedTokens += *ctxWin.TotalOutputTokens
		}
		if ctxWin.ContextWindowSize != nil {
			ctxSize = *ctxWin.ContextWindowSize
		}
	}
	hasActiveCtx := ctxWin != nil && (usedTokens > 0 || usedPct > 0) && ctxSize > 0
	collapsed := st.Prefs.AGCollapsedSpeed

	ui.Column(c).FillWidth().Padding(12, 0).Gap(8).Children(func() {
		SectionHeader(c, "ag-sec-speed", tr(zh, "输出速度", "Output speed"), collapsed, func() {
			m.Store.UpdatePrefs(func(p *core.Preferences) { p.AGCollapsedSpeed = !p.AGCollapsedSpeed })
		}, func() {
			if collapsed {
				if tok := speed.DisplayTokPerSec(); tok != nil {
					ui.Text(c, fmt.Sprintf("~%d tok/s", int(math.Round(*tok)))).FontSize(10.5).FontWeight(500).
						TextColor(ColorSecondary).FontFeatures("tnum").SingleLine()
				} else {
					ui.Text(c, tr(zh, "最近 1 小时没有回复", "No replies in 1h")).FontSize(10.5).TextColor(ColorTertiary).SingleLine()
				}
				return
			}
			if speed.IsRecentlyActive && speed.LastResponseAgeSec != nil {
				age := int(math.Round(*speed.LastResponseAgeSec))
				ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Size(5, 5).Radius(2.5).Background(ColorLive)
					ui.Text(c, tr(zh, fmt.Sprintf("生成活跃 · %ds 前", age), fmt.Sprintf("Active · %ds ago", age))).
						FontSize(10.5).TextColor(ColorSecondary).FontFeatures("tnum").SingleLine()
				})
			} else if st.AG.OfficialReport.ActiveModelName != "" && hasActiveCtx {
				ui.Text(c, st.AG.OfficialReport.ActiveModelName).FontSize(10.5).TextColor(ColorTertiary).SingleLine()
			}
		})

		if collapsed {
			return
		}

		tokPtr := speed.DisplayTokPerSec()
		if tokPtr == nil {
			EmptyNote(c, IconPulse, tr(zh, "最近 1 小时没有回复", "No replies in the last hour"),
				tr(zh, "Antigravity 写完回复后，这里显示估算的每秒输出 token", "Approximate tokens per second appear here after a reply."), nil)
			return
		}

		recentSpark := speed.Samples
		if len(recentSpark) > 18 {
			recentSpark = recentSpark[len(recentSpark)-18:]
		}

		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Row(c).AlignItems(ui.Baseline).Gap(3).
					Tooltip(tr(zh, "基于本地对话间隔近似估算。点击右侧图表可切换折线图与 1 小时散点图。",
						"Estimated from local transcript intervals. Click chart to toggle sparkline / 1h dot plot.")).
					Children(func() {
						ui.Text(c, fmt.Sprintf("~%d", int(math.Round(*tokPtr)))).
							FontSize(25).FontWeight(600).LetterSpacing(-0.4).TextColor(ColorText).FontFeatures("tnum").SingleLine()
						ui.Text(c, "tok/s").FontSize(11.5).TextColor(ColorSecondary).SingleLine()
					})

				if m.agHoveredSpark >= 0 && m.agHoveredSpark < len(recentSpark) {
					hs := recentSpark[m.agHoveredSpark]
					ui.Text(c, fmt.Sprintf("%s · ~%d tok/s · %s",
						hs.Timestamp.Local().Format("15:04"), int(math.Round(hs.TokensPerSec)), core.FormatMagnitude(hs.OutputTokens, zh))).
						FontSize(10.5).TextColor(ColorSecondary).FontFeatures("tnum").SingleLine()
				} else {
					winLbl := tr(zh, "近 1 小时", "Last 1 hr")
					if speed.Recent5mTokPerSec != nil {
						winLbl = tr(zh, "近 5 分钟", "Last 5 min")
					}
					if speed.LatestModelName != "" {
						winLbl += " · " + speed.LatestModelName
					}
					ui.Text(c, winLbl).FontSize(10.5).TextColor(ColorTertiary).SingleLine()
				}
			})

			if m.agShowDotChart {
				btn := ui.Column(c.Key("ag-spd-back")).AlignItems(ui.End).Gap(2).Cursor(ui.CursorPointer)
				if btn.Clicked() {
					m.agShowDotChart = false
					m.agHoveredDotID = 0
				}
				btn.Children(func() {
					if speed.Recent1hTokPerSec != nil {
						ui.Text(c, fmt.Sprintf("~%d tok/s", int(math.Round(*speed.Recent1hTokPerSec)))).
							FontSize(12).FontWeight(500).TextColor(ColorSecondary).FontFeatures("tnum").SingleLine()
					}
					ui.Text(c, tr(zh, fmt.Sprintf("近 1 小时 · %d 次", speed.Recent1hReplyCount), fmt.Sprintf("Last 1h · %d", speed.Recent1hReplyCount))).
						FontSize(10).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
				})
			} else if len(speed.Samples) >= 2 {
				AGSparkline(c, "ag-spd-spark", speed.Samples, &m.agHoveredSpark, func() {
					m.agHoveredSpark = -1
					m.agShowDotChart = true
				})
			}
		})

		if m.agShowDotChart && len(speed.Samples) > 0 {
			ui.Column(c).FillWidth().Gap(4).Children(func() {
				AGSpeedDotChart(c, "ag-spd-dot", speed.Samples, st.AG.LocalReport.UpdatedAt, &m.agHoveredDotID, func() {
					m.agHoveredDotID = 0
					m.agShowDotChart = false
				})
				var hovDot *core.AGSpeedSample
				if m.agHoveredDotID > 0 {
					for i := range speed.Samples {
						if speed.Samples[i].ID == m.agHoveredDotID {
							hovDot = &speed.Samples[i]
							break
						}
					}
				}
				if hovDot != nil {
					ui.Text(c, fmt.Sprintf("%s · ~%d tok/s · %s tokens · %s",
						hovDot.Timestamp.Local().Format("15:04"), int(math.Round(hovDot.TokensPerSec)),
						core.FormatMagnitude(hovDot.OutputTokens, zh), hovDot.ModelDisplayName)).
						FontSize(10).TextColor(ColorSecondary).FontFeatures("tnum").SingleLine()
				} else {
					ui.Row(c).FillWidth().Children(func() {
						ui.Text(c, tr(zh, "1 小时前", "1h ago")).FontSize(9.5).TextColor(ColorTertiary).SingleLine()
						ui.Spacer(c)
						ui.Text(c, tr(zh, "点击图表切换折线", "Click to toggle sparkline")).FontSize(9.5).TextColor(ColorQuaternary).SingleLine()
						ui.Spacer(c)
						ui.Text(c, tr(zh, "现在", "Now")).FontSize(9.5).TextColor(ColorTertiary).SingleLine()
					})
				}
			})
		}

		if hasActiveCtx && st.Prefs.SpeedDisplay == core.DisplayDetailed {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, tr(zh, "活跃上下文", "Context")).FontSize(10.5).TextColor(ColorTertiary).SingleLine()
				ui.Spacer(c)
				ui.Text(c, fmt.Sprintf("%.1f%% · %s / %s", usedPct,
					core.FormatMagnitude(usedTokens, zh), core.FormatMagnitude(ctxSize, zh))).
					FontSize(10.5).TextColor(ColorSecondary).FontFeatures("tnum").SingleLine()
			})
		}
	})
}

func (m *PopoverModel) renderAGSpendSection(c *ui.Context, st service.ViewState, now time.Time, zh bool) {
	local := st.AG.LocalReport
	off := st.AG.OfficialReport
	money := core.NewMoneyFormat(st.Prefs.Currency)
	span := st.Prefs.AGCostSpan
	if span == "" {
		span = core.SpanWeek
	}
	collapsed := st.Prefs.AGCollapsedSpend
	summary := local.SummaryFor(span)
	models := local.ModelsFor(span)

	var period *core.AGBillingPeriodInfo
	switch span {
	case core.SpanMonth:
		period = local.MonthPeriod
	case core.SpanWeek:
		period = local.WeekPeriod
	}

	ui.Column(c).FillWidth().Padding(12, 0).Gap(10).Children(func() {
		SectionHeader(c, "ag-sec-spend", tr(zh, "本期消耗", "Spend"), collapsed, func() {
			m.Store.UpdatePrefs(func(p *core.Preferences) { p.AGCollapsedSpend = !p.AGCollapsedSpend })
		}, func() {
			if collapsed {
				ui.Row(c).Gap(4).Children(func() {
					ui.Text(c, spanShortTitle(span, zh)).FontSize(10.5).TextColor(ColorTertiary).SingleLine()
					ui.Text(c, money.Format(summary.EstimatedCostUSD)).FontSize(10.5).FontWeight(500).
						TextColor(ColorSecondary).FontFeatures("tnum").SingleLine()
				})
				return
			}
			if span == core.SpanMonth && period == nil {
				btn := ui.Row(c.Key("ag-set-bday")).Cursor(ui.CursorPointer)
				if btn.Clicked() && m.Actions.OpenSettings != nil {
					m.Actions.OpenSettings()
				}
				btn.Children(func() {
					ui.Text(c, tr(zh, "设置订阅日…", "Set billing day…")).FontSize(10.5).FontWeight(500).TextColor(ColorAGAccent).SingleLine()
				})
				return
			}
			if period != nil {
				ui.Text(c, core.FormatPeriodRange(period.Start, period.End.Add(time.Second), zh)).
					FontSize(10.5).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
			} else {
				ui.Text(c, core.FormatDayWithWeekday(local.UpdatedAt, zh)).
					FontSize(10.5).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
			}
		})

		if collapsed {
			return
		}

		selIdx := 1
		switch span {
		case core.SpanMonth:
			selIdx = 0
		case core.SpanWeek:
			selIdx = 1
		case core.SpanDay:
			selIdx = 2
		}
		SegmentedTabs(c, "ag-spend-tabs", []string{
			tr(zh, "本月周期", "This month"),
			tr(zh, "本周期", "This week"),
			tr(zh, "每日", "Today"),
		}, selIdx, true, func(idx int) {
			m.Store.UpdatePrefs(func(p *core.Preferences) {
				p.AGCostSpan = core.AllCostSpans[idx]
			})
		})

		// Projection based on official weekly usage fraction
		var projCost *float64
		if span != core.SpanDay && off.PrimaryWeekly != nil && !off.PrimaryWeekly.IsResetElapsed &&
			off.PrimaryWeekly.UsedFraction >= 0.01 && local.Week.EstimatedCostUSD > 0 {
			fullWeek := local.Week.EstimatedCostUSD / off.PrimaryWeekly.UsedFraction
			v := fullWeek
			if span == core.SpanMonth {
				days := 30.0
				if local.MonthPeriod != nil && local.MonthPeriod.TotalDays > 0 {
					days = float64(local.MonthPeriod.TotalDays)
				}
				v = fullWeek * (days / 7.0)
			}
			projCost = &v
		}

		// Big amount + projected + 14-day bars
		ui.Row(c).FillWidth().AlignItems(ui.End).Gap(8).Children(func() {
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				amtRow := ui.Row(c.Key("ag-spend-amt")).AlignItems(ui.Baseline).Gap(6).Cursor(ui.CursorPointer)
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
				amtRow.Tooltip(tr(zh, "API 等价花费估算（按公开单价折算本地对话日志）。点击金额切换货币。",
					"API-equivalent spend estimate from local logs. Click amount to cycle currency."))
				amtRow.Children(func() {
					ui.Text(c, money.Format(summary.EstimatedCostUSD)).FontSize(26).FontWeight(600).LetterSpacing(-0.4).TextColor(ColorText).FontFeatures("tnum").SingleLine()
					if projCost != nil {
						ui.Text(c, tr(zh, "预计 "+money.Whole(*projCost), "→ "+money.Whole(*projCost))).
							FontSize(12).FontWeight(500).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
					}
				})

				if m.agHoveredBar >= 0 && m.agHoveredBar < len(local.DailyTrend) {
					d := local.DailyTrend[m.agHoveredBar]
					ui.Text(c, fmt.Sprintf("%s · %s · %s", core.FormatDayWithWeekday(d.Date, zh),
						core.FormatMagnitude(d.TotalTokens(), zh), money.Format(d.EstimatedCostUSD))).
						FontSize(10.5).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
				} else if summary.HasUnpricedModels() {
					ui.Text(c, tr(zh, "含未定价模型: "+strings.Join(summary.UnpricedModels, ", "),
						"Includes unpriced: "+strings.Join(summary.UnpricedModels, ", "))).
						FontSize(10).TextColor(ColorWarning).SingleLine()
				} else if period != nil {
					dayLbl := tr(zh, fmt.Sprintf("第 %d / %d 天", period.CurrentDay, period.TotalDays),
						fmt.Sprintf("Day %d of %d", period.CurrentDay, period.TotalDays))
					extra := ""
					if projCost != nil {
						left := max(0.0, *projCost-summary.EstimatedCostUSD)
						extra = " · " + tr(zh, "预计还剩 "+money.Whole(left), money.Whole(left)+" left")
					} else if summary.CacheSavingsUSD > 0 {
						extra = " · " + tr(zh, "省 "+money.Format(summary.CacheSavingsUSD), "Saved "+money.Format(summary.CacheSavingsUSD))
					}
					ui.Text(c, dayLbl+extra).FontSize(10.5).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
				} else if summary.CacheSavingsUSD > 0 {
					ui.Text(c, tr(zh, "省 "+money.Format(summary.CacheSavingsUSD), "Saved "+money.Format(summary.CacheSavingsUSD))).
						FontSize(10.5).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
				}
			})

			if st.Prefs.SpendDisplay == core.DisplayDetailed && len(local.DailyTrend) > 0 {
				vals := make([]float64, len(local.DailyTrend))
				for i, d := range local.DailyTrend {
					vals[i] = float64(d.TotalTokens())
				}
				MiniBarsColored(c, "ag-spend-bars", vals, len(vals)-1, -1, ColorAGAccent, &m.agHoveredBar)
			}
		})

		if st.Prefs.SpendDisplay == core.DisplayCompact {
			return
		}

		// Summary stats row: 总 Tokens | 请求 | 今日
		ui.Row(c).FillWidth().AlignItems(ui.Baseline).Children(func() {
			ui.Row(c).Gap(5).AlignItems(ui.Baseline).Children(func() {
				ui.Text(c, tr(zh, "总 Tokens", "Tokens")).FontSize(10.5).TextColor(ColorTertiary).SingleLine()
				ui.Text(c, core.FormatMagnitude(summary.TotalTokens(), zh)).FontSize(12.5).FontWeight(500).TextColor(ColorText).FontFeatures("tnum").SingleLine()
			}).Tooltip(fmt.Sprintf("%s tokens", core.FormatGrouped(summary.TotalTokens())))
			ui.Spacer(c)
			ui.Row(c).Gap(5).AlignItems(ui.Baseline).Children(func() {
				ui.Text(c, tr(zh, "请求", "Requests")).FontSize(10.5).TextColor(ColorTertiary).SingleLine()
				ui.Text(c, tr(zh, core.FormatGrouped(int64(summary.RequestCount))+" 次", core.FormatGrouped(int64(summary.RequestCount)))).
					FontSize(12.5).FontWeight(500).TextColor(ColorText).FontFeatures("tnum").SingleLine()
			})
			ui.Spacer(c)
			ui.Row(c).Gap(5).AlignItems(ui.Baseline).Children(func() {
				ui.Text(c, tr(zh, "今日", "Today")).FontSize(10.5).TextColor(ColorTertiary).SingleLine()
				ui.Text(c, money.Format(local.Today.EstimatedCostUSD)).FontSize(12.5).FontWeight(500).TextColor(ColorText).FontFeatures("tnum").SingleLine()
			})
		})

		// 3-column token breakdown with colored dots (matching Antigravity transcript schema)
		ui.Row(c).FillWidth().AlignItems(ui.Start).Children(func() {
			renderAGTokenCol(c, "ag-tk-in", tr(zh, "新增输入", "Input"), ColorInputTok, summary.UncachedInputTokens, zh)
			renderAGTokenCol(c, "ag-tk-out", tr(zh, "输出", "Output"), ColorOutputTok, summary.OutputTokens, zh)
			renderAGTokenCol(c, "ag-tk-cr", tr(zh, "缓存命中", "Cache read"), ColorAGAccent, summary.CacheReadTokens, zh)
		})

		// Cache hit rate bar
		hitRate := summary.CacheHitRate()
		ui.Column(c).FillWidth().Gap(3).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Baseline).Gap(6).Children(func() {
				ui.Text(c, tr(zh, "缓存命中率", "Cache hit rate")).FontSize(12.5).TextColor(ColorText).SingleLine()
				ui.Spacer(c)
				if summary.CacheSavingsUSD > 0 {
					ui.Text(c, tr(zh, "省 "+money.Format(summary.CacheSavingsUSD), "Saved "+money.Format(summary.CacheSavingsUSD))).
						FontSize(10.5).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
				}
				hitTxt := "--"
				if summary.TotalInputTokens() > 0 {
					hitTxt = core.FormatPercent(hitRate, 1)
				}
				ui.Text(c, hitTxt).FontSize(12.5).FontWeight(500).TextColor(ColorText).FontFeatures("tnum").SingleLine()
			})
			ThinBar(c, "ag-cache-hit-bar", hitRate, ui.RGBA(255, 255, 255, 0.45), nil, false, nil)
		})

		// Top 3 model breakdown
		if len(models) > 0 {
			m.renderAGModelList(c, models, money, zh)
		}
	})
}

func renderAGTokenCol(c *ui.Context, key, label string, dotColor ui.Color, tokens int64, zh bool) {
	col := ui.Column(c.Key(key)).Grow(1).Gap(4)
	col.Tooltip(fmt.Sprintf("%s: %s tokens", label, core.FormatGrouped(tokens)))
	col.Children(func() {
		ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(5, 5).Radius(2.5).Background(dotColor)
			ui.Text(c, label).FontSize(10.5).TextColor(ColorTertiary).SingleLine()
		})
		ui.Text(c, core.FormatMagnitude(tokens, zh)).FontSize(13).FontWeight(500).TextColor(ColorText).FontFeatures("tnum").SingleLine()
	})
}

func (m *PopoverModel) renderAGModelList(c *ui.Context, models []core.AGModelUsageSummary, money core.MoneyFormat, zh bool) {
	var totalTok int64
	for _, mod := range models {
		totalTok += mod.TotalTokens()
	}
	if totalTok <= 0 {
		totalTok = 1
	}
	keep := min(3, len(models))
	ui.Column(c).FillWidth().Gap(5).Margin(2, 0, 0, 0).Children(func() {
		for i := 0; i < keep; i++ {
			mod := models[i]
			share := float64(mod.TotalTokens()) / float64(totalTok)
			row := ui.Row(c.Key("ag-mod-"+mod.Model)).FillWidth().Gap(8).AlignItems(ui.Center)
			tipLines := []string{
				mod.DisplayName,
				fmt.Sprintf("%s: %s · %s: %s · %s: %s",
					tr(zh, "输入", "In"), core.FormatMagnitude(mod.UncachedInputTokens, zh),
					tr(zh, "输出", "Out"), core.FormatMagnitude(mod.OutputTokens, zh),
					tr(zh, "缓存", "Cache"), core.FormatMagnitude(mod.CacheReadTokens, zh)),
				fmt.Sprintf("%s: %s · %s: %d",
					tr(zh, "命中率", "Cache"), core.FormatPercent(mod.CacheHitRate(), 1),
					tr(zh, "请求", "Reqs"), mod.RequestCount),
			}
			if mod.CacheSavingsUSD != nil && *mod.CacheSavingsUSD > 0 {
				tipLines = append(tipLines, tr(zh, "缓存节省: ", "Cache saved: ")+money.Format(*mod.CacheSavingsUSD))
			}
			row.Tooltip(strings.Join(tipLines, "\n"))
			row.Children(func() {
				ui.Text(c, mod.DisplayName).FontSize(11.5).TextColor(ColorSecondary).SingleLine().Grow(1)
				ui.Text(c, core.FormatPercent(share, 1)).Width(44).TextAlign(ui.End).FontSize(11.5).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
				if mod.EstimatedCostUSD != nil {
					ui.Text(c, money.Format(*mod.EstimatedCostUSD)).MinWidth(60).TextAlign(ui.End).FontSize(11.5).TextColor(ColorText).FontFeatures("tnum").SingleLine()
				} else {
					ui.Text(c, tr(zh, "未定价", "Unpriced")).MinWidth(60).TextAlign(ui.End).FontSize(10.5).TextColor(ColorWarning).SingleLine()
				}
			})
		}
	})
}
