package appui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"ccmonitor/internal/core"
	"ccmonitor/internal/platform"
	"ccmonitor/internal/service"
)

type SettingsTab int

const (
	TabUsage SettingsTab = iota
	TabMenuBar
	TabPanel
	TabData
	TabGeneral
)

type SettingsActions struct {
	ChooseClaudeDir    func()
	ChooseDir          func()
	ChooseAGBrainDir   func()
	ChooseAgyBinary    func()
	ToggleFloatingPill func(show bool)
	SetPillAlwaysOnTop func(on bool)
	SyncPillSize       func()
	SyncTray           func()
	OnProviderChanged  func()
	GetOpenAtLogin     func() bool
	SetOpenAtLogin     func(bool) error
	OpenURL            func(url string)
}

type SettingsModel struct {
	Store   *service.Store
	Actions SettingsActions

	ActiveTab              SettingsTab
	updateNotesExpanded    bool
	menuBarPreviewProvider core.ProviderKind
	menuBarPreviewSynced   bool
	proxyDraft             string
	proxySynced            bool
	intervalStr            string
	intSynced              bool
	claudeDirDraft         string
	lastClaudePref         string
	claudeDirSynced        bool
	agBrainDraft           string
	lastAGBrainPref        string
	agBrainSynced          bool
	agCLIDraft             string
	lastAGCLIPref          string
	agCLISynced            bool
	openAtLogin            bool
	loginSynced            bool
}

func NewSettingsModel(store *service.Store, actions SettingsActions) *SettingsModel {
	return &SettingsModel{
		Store:     store,
		Actions:   actions,
		ActiveTab: TabUsage,
	}
}

func (m *SettingsModel) OpenUpdateSection() {
	m.ActiveTab = TabGeneral
	m.updateNotesExpanded = true
}

func (m *SettingsModel) View(c *ui.Context) {
	st := m.Store.ViewState()
	zh := st.Prefs.IsChinese()
	ApplyThemeFor(c, st.Prefs.ActiveProvider)
	c.Root().Background(ColorSurfaceSettings)

	if !m.menuBarPreviewSynced {
		m.menuBarPreviewProvider = core.NormalizeProvider(st.Prefs.ActiveProvider)
		m.menuBarPreviewSynced = true
	}
	if !m.proxySynced {
		m.proxyDraft = st.Prefs.ProxyURL
		m.proxySynced = true
	}
	if !m.intSynced {
		m.intervalStr = strconv.Itoa(st.Prefs.SyncIntervalSec)
		m.intSynced = true
	}
	if !m.claudeDirSynced || st.Prefs.ClaudeConfigDir != m.lastClaudePref {
		m.claudeDirDraft = st.Prefs.ClaudeConfigDir
		m.lastClaudePref = st.Prefs.ClaudeConfigDir
		m.claudeDirSynced = true
	}
	if !m.agBrainSynced || st.Prefs.AGBrainDir != m.lastAGBrainPref {
		m.agBrainDraft = st.Prefs.AGBrainDir
		m.lastAGBrainPref = st.Prefs.AGBrainDir
		m.agBrainSynced = true
	}
	if !m.agCLISynced || st.Prefs.AGAgyCLIPath != m.lastAGCLIPref {
		m.agCLIDraft = st.Prefs.AGAgyCLIPath
		m.lastAGCLIPref = st.Prefs.AGAgyCLIPath
		m.agCLISynced = true
	}
	if !m.loginSynced {
		if m.Actions.GetOpenAtLogin != nil {
			m.openAtLogin = m.Actions.GetOpenAtLogin()
		}
		m.loginSynced = true
	}

	ui.Column(c).Fill().Background(ColorSurfaceSettings).Children(func() {
		// Top 5-tab icon bar matching hero.png / settings-panel.png / settings-usage.png
		m.renderTabBar(c, st, zh)
		Hairline(c)

		// Scrollable settings body
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Padding(20, 24, 28, 24).Gap(16).Children(func() {
				switch m.ActiveTab {
				case TabUsage:
					m.renderUsageTab(c, st, zh)
				case TabMenuBar:
					m.renderMenuBarTab(c, st, zh)
				case TabPanel:
					m.renderPanelTab(c, st, zh)
				case TabData:
					m.renderDataTab(c, st, zh)
				case TabGeneral:
					m.renderGeneralTab(c, st, zh)
				}
			})
		})
	})
}

func (m *SettingsModel) renderTabBar(c *ui.Context, st service.ViewState, zh bool) {
	tabs := []struct {
		id   SettingsTab
		icon *ui.SVG
		name string
	}{
		{TabUsage, IconGauge, tr(zh, "用量", "Usage")},
		{TabMenuBar, IconMenuBar, tr(zh, "菜单栏", "Menubar")},
		{TabPanel, IconPanel, tr(zh, "面板", "Panel")},
		{TabData, IconDB, tr(zh, "数据", "Data")},
		{TabGeneral, IconSliders, tr(zh, "通用", "General")},
	}

	ui.Row(c).FillWidth().Padding(10, 16).Center().Gap(8).Background(ColorSurfaceHeader).Children(func() {
		for _, tb := range tabs {
			active := m.ActiveTab == tb.id
			btn := ui.Column(c.Key(fmt.Sprintf("stab-%d", tb.id))).
				Width(68).Padding(7, 8).Radius(10).Center().Gap(4).Cursor(ui.CursorPointer)
			if active {
				btn.Background(ui.RGBA(255, 255, 255, 0.11))
			} else if btn.Hovered() {
				btn.Background(ColorHover)
			}
			if btn.Clicked() {
				m.ActiveTab = tb.id
			}
			col := ColorSecondary
			if active {
				col = ColorText
			}
			btn.Children(func() {
				ui.Icon(c, tb.icon).FontSize(17).TextColor(col)
				ui.Row(c).Center().Gap(4).Children(func() {
					ui.Text(c, tb.name).FontSize(11).FontWeight(500).TextColor(col).SingleLine()
					if tb.id == TabGeneral && st.Update.HasBadge() {
						ui.Box(c).Size(6, 6).Radius(3).Background(ColorAGAccent)
					}
				})
			})
		}
	})
}

func settingsGroupTitle(c *ui.Context, title string) {
	ui.Text(c, title).FontSize(11.5).FontWeight(500).TextColor(ColorTertiary).Margin(4, 0, -4, 0)
}

func settingsRow(c *ui.Context, title, desc string, control func()) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(16).Children(func() {
		ui.Column(c).Grow(1).Gap(3).Children(func() {
			ui.Text(c, title).FontSize(13).FontWeight(500).TextColor(ColorText)
			if desc != "" {
				ui.Text(c, desc).FontSize(11.5).TextColor(ColorTertiary)
			}
		})
		if control != nil {
			control()
		}
	})
}

func (m *SettingsModel) renderUsageTab(c *ui.Context, st service.ViewState, zh bool) {
	settingsGroupTitle(c, tr(zh, "官方用量", "Official usage"))

	// Status subtitle
	statusSub := tr(zh, "与 Claude Code /usage 一致", "Matches Claude Code /usage")
	if st.OfficialUsage != nil {
		rel := core.FormatRelative(st.OfficialUsage.FetchedAt, time.Now(), zh)
		planTxt := st.DetectedPlan.Title()
		if planTxt != "" {
			planTxt = " · " + planTxt
		}
		statusSub = tr(zh, fmt.Sprintf("已连接%s · 上次同步 %s · 与 Claude Code /usage 一致", planTxt, rel),
			fmt.Sprintf("Connected%s · Synced %s", planTxt, rel))
	}
	settingsRow(c, tr(zh, "Claude 官方用量", "Official Claude usage"), statusSub, func() {
		TerracottaSwitch(c, "sw-off", st.Prefs.OfficialEnabled, func(on bool) {
			m.Store.UpdatePrefs(func(p *core.Preferences) { p.OfficialEnabled = on })
		})
	})

	Hairline(c)

	settingsRow(c, tr(zh, "展开面板时立即查询", "Check when opening panel"),
		tr(zh, "点开托盘图标时，上次同步后的消耗可能已经让数字变了，就立即查询；否则直接显示上次的结果，不发请求。",
			"Refreshes when opening the panel if local usage likely moved the percentage."), func() {
			TerracottaSwitch(c, "sw-open", st.Prefs.CheckOnOpen, func(on bool) {
				m.Store.UpdatePrefs(func(p *core.Preferences) { p.CheckOnOpen = on })
			})
		})

	Hairline(c)

	settingsRow(c, tr(zh, "查询间隔", "Sync interval"),
		tr(zh, "Claude Code 正在使用时，消耗够让官方百分比涨一格才查，两次至少隔这么久（10–3600 秒）。",
			"Minimum seconds between automatic checks while active (10–3600s)."), func() {
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				inp := ui.TextInput(c.Key("in-interval"), &m.intervalStr).Width(68).FontSize(12.5).TextColor(ColorText)
				if inp.Submitted() || inp.Changed() {
					if v, err := strconv.Atoi(strings.TrimSpace(m.intervalStr)); err == nil && v >= 10 && v <= 3600 {
						m.Store.UpdatePrefs(func(p *core.Preferences) { p.SyncIntervalSec = v })
					}
				}
				ui.Text(c, tr(zh, "秒", "s")).FontSize(11.5).TextColor(ColorTertiary)
			})
		})

	Hairline(c)

	settingsRow(c, tr(zh, "官方请求代理", "Official API proxy"),
		tr(zh, "留空使用 Windows 系统代理。填写后用量和续期都从这里出去，例如 127.0.0.1:7890 或 socks5://127.0.0.1:7890",
			"Leave blank for Windows system proxy. Example: 127.0.0.1:7890 or socks5://127.0.0.1:7890"), func() {
			inp := ui.TextInput(c.Key("in-proxy"), &m.proxyDraft).
				Width(195).Placeholder("127.0.0.1:7890").FontSize(12)
			if inp.Submitted() || inp.Changed() {
				val := strings.TrimSpace(m.proxyDraft)
				if val != st.Prefs.ProxyURL {
					m.Store.UpdatePrefs(func(p *core.Preferences) { p.ProxyURL = val })
				}
			}
		})

	Hairline(c)

	settingsRow(c, tr(zh, "自动续期登录", "Auto-renew login"),
		tr(zh, "默认开启。登录快过期时，使用与 Claude Code 相同的目录锁自动续期并写回凭据。",
			"Renews OAuth token 5 minutes before expiry using Claude Code's lock protocol."), func() {
			TerracottaSwitch(c, "sw-renew", st.Prefs.AutoRenewLogin, func(on bool) {
				m.Store.UpdatePrefs(func(p *core.Preferences) { p.AutoRenewLogin = on })
			})
		})

	Hairline(c)

	// Credentials file status (reads <claudeDir>\.credentials.json only)
	acctDesc := tr(zh, fmt.Sprintf("未在 %s 中找到 .credentials.json，可在「数据」页修改 .claude 目录或通过 Claude Code 终端登录。", st.ClaudeConfigDir),
		fmt.Sprintf("No .credentials.json found in %s. Set the .claude directory under Data or sign in via Claude Code.", st.ClaudeConfigDir))
	if st.AccountEmail != "" {
		acctDesc = tr(zh, fmt.Sprintf("当前账号：%s（读取自 %s\\.credentials.json）", st.AccountEmail, st.ClaudeConfigDir),
			fmt.Sprintf("Signed in: %s (from %s\\.credentials.json)", st.AccountEmail, st.ClaudeConfigDir))
	} else if st.HasCredentialsFile {
		acctDesc = tr(zh, fmt.Sprintf("已检测到凭据文件：%s\\.credentials.json", st.ClaudeConfigDir),
			fmt.Sprintf("Credentials file detected: %s\\.credentials.json", st.ClaudeConfigDir))
	}
	settingsRow(c, tr(zh, "本地凭据文件", "Local credentials file"), acctDesc, func() {
		b := ui.Row(c.Key("btn-set-cli")).Height(28).Padding(0, 12).Radius(7).Center().
			Background(ColorPillBg).Cursor(ui.CursorPointer)
		if b.Clicked() {
			m.Store.LaunchClaudeCodeLogin()
		}
		b.Children(func() {
			ui.Text(c, tr(zh, "终端登录", "CLI Login")).FontSize(11.5).FontWeight(500).TextColor(ColorText).SingleLine()
		})
	})

	ui.Text(c, tr(zh,
		"官方用量会用当前登录访问 Anthropic。自动查询只在出口可用、Claude Code 正在使用时进行；Token 没有变化时不发请求。遇到 429 自动退避，并始终为「立即刷新」保留配额。",
		"Official usage queries api.anthropic.com directly. Automatic sync only runs when active and exit is safe, with token-bucket rate-limit protection.")).
		FontSize(11).TextColor(ColorTertiary)

	settingsGroupTitle(c, tr(zh, "计费周期", "Billing cycle"))

	dayOpts := make([]string, 32)
	dayOpts[0] = tr(zh, "不设置", "Not set")
	for d := 1; d <= 31; d++ {
		dayOpts[d] = tr(zh, fmt.Sprintf("每月 %d 日", d), fmt.Sprintf("Day %d", d))
	}
	curDay := dayOpts[0]
	if st.Prefs.BillingAnchorDay >= 1 && st.Prefs.BillingAnchorDay <= 31 {
		curDay = dayOpts[st.Prefs.BillingAnchorDay]
	}
	rangeDesc := tr(zh, "设置后在顶部套餐徽章旁显示本期还剩几天", "Shows days left beside the plan badge")
	if st.Snapshot != nil && st.Prefs.BillingAnchorDay > 0 {
		rangeDesc = tr(zh, "当前周期 ", "Current period ") + core.FormatPeriodRange(st.Snapshot.Billing.Start, st.Snapshot.Billing.End, zh)
	}
	settingsRow(c, tr(zh, "每月扣费日", "Billing day"), rangeDesc, func() {
		sel := curDay
		if ui.Select(c.Key("sel-anchor"), &sel, dayOpts).Width(120).Changed() {
			for i, o := range dayOpts {
				if o == sel {
					m.Store.UpdatePrefs(func(p *core.Preferences) { p.BillingAnchorDay = i })
					break
				}
			}
		}
	})

	Hairline(c)

	settingsGroupTitle(c, tr(zh, "Antigravity 官方额度（agy CLI）", "Antigravity official limits (agy CLI)"))

	agSub := tr(zh, "通过官方 agy --settings 在后台静默获取 5 小时与每周额度（零抓包）",
		"Silently syncs 5h & weekly limits via official agy --settings (zero sniffing)")
	if st.AG.OfficialReport.HasSnapshot() && st.AG.OfficialReport.SnapshotCapturedAt != nil {
		rel := core.FormatRelative(*st.AG.OfficialReport.SnapshotCapturedAt, time.Now(), zh)
		acct := strings.TrimSpace(st.AG.OfficialReport.Email)
		if acct != "" {
			acct = " · " + acct
		}
		tier := st.AG.OfficialReport.PlanTier
		if tier != "" {
			tier = " · " + strings.ToUpper(tier)
		}
		agSub = tr(zh, fmt.Sprintf("已读取快照%s%s · 上次同步 %s", acct, tier, rel),
			fmt.Sprintf("Snapshot ready%s%s · Synced %s", acct, tier, rel))
	}
	settingsRow(c, tr(zh, "Antigravity 后台静默同步", "Antigravity background sync"), agSub, func() {
		AccentSwitch(c, "sw-ag-off", st.Prefs.AGOfficialEnabled, ColorAGAccent, func(on bool) {
			m.Store.UpdatePrefs(func(p *core.Preferences) { p.AGOfficialEnabled = on })
		})
	})

	Hairline(c)

	agIntVals := []int{10, 30, 60, 300}
	agIntIdx := 1
	for i, v := range agIntVals {
		if st.Prefs.AGSyncIntervalSec == v {
			agIntIdx = i
			break
		}
	}
	settingsRow(c, tr(zh, "Antigravity 同步间隔", "Antigravity sync interval"),
		tr(zh, "后台检查与刷新快照频率", "Interval for background snapshot & quota refresh."), func() {
			SegmentedTabs(c, "seg-ag-int", []string{"10s", "30s", "60s", "5m"}, agIntIdx, false, func(i int) {
				m.Store.UpdatePrefs(func(p *core.Preferences) { p.AGSyncIntervalSec = agIntVals[i] })
			})
		})

	Hairline(c)

	// agy CLI binary path + detect status
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		cliDesc := tr(zh, "未检测到 agy CLI，留空自动从 PATH 与常见目录探测，或手动指定可执行路径。",
			"agy CLI not found. Leave blank to auto-detect from PATH, or browse to agy.exe / agy.cmd.")
		if st.AG.CLIBinaryPath != "" {
			cliDesc = tr(zh, "已检测到：", "Detected: ") + st.AG.CLIBinaryPath
		}
		ui.Text(c, tr(zh, "agy CLI 路径", "agy CLI path")).FontSize(13).FontWeight(500).TextColor(ColorText)
		ui.Text(c, cliDesc).FontSize(11.5).TextColor(ColorTertiary)

		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			inp := ui.TextInput(c.Key("in-ag-cli"), &m.agCLIDraft).Grow(1).Placeholder("agy").FontSize(12)
			if inp.Submitted() {
				val := strings.TrimSpace(m.agCLIDraft)
				m.Store.UpdatePrefs(func(p *core.Preferences) { p.AGAgyCLIPath = val })
			}
			applyBtn := ui.Row(c.Key("btn-apply-ag-cli")).Height(28).Padding(0, 12).Radius(7).Center().
				Background(ColorAGAccent.Alpha(0.22)).Cursor(ui.CursorPointer)
			if applyBtn.Clicked() {
				val := strings.TrimSpace(m.agCLIDraft)
				m.Store.UpdatePrefs(func(p *core.Preferences) { p.AGAgyCLIPath = val })
			}
			applyBtn.Children(func() {
				ui.Text(c, tr(zh, "应用", "Apply")).FontSize(12).FontWeight(500).TextColor(ColorAGAccent).SingleLine()
			})
			if m.Actions.ChooseAgyBinary != nil {
				pickBtn := ui.Row(c.Key("btn-pick-ag-cli")).Height(28).Padding(0, 12).Radius(7).Center().
					Background(ColorPillSel).Cursor(ui.CursorPointer)
				if pickBtn.Clicked() {
					m.Actions.ChooseAgyBinary()
				}
				pickBtn.Children(func() {
					ui.Text(c, tr(zh, "浏览…", "Browse…")).FontSize(12).TextColor(ColorText).SingleLine()
				})
			}
			if strings.TrimSpace(st.Prefs.AGAgyCLIPath) != "" {
				clrBtn := ui.Row(c.Key("btn-clr-ag-cli")).Height(28).Padding(0, 10).Radius(7).Center().
					Background(ColorPillBg).Cursor(ui.CursorPointer)
				if clrBtn.Clicked() {
					m.agCLIDraft = ""
					m.Store.UpdatePrefs(func(p *core.Preferences) { p.AGAgyCLIPath = "" })
				}
				clrBtn.Children(func() {
					ui.Text(c, tr(zh, "自动", "Auto")).FontSize(11.5).TextColor(ColorSecondary).SingleLine()
				})
			}
		})

		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			syncBtn := ui.Row(c.Key("btn-ag-sync-now")).Height(28).Padding(0, 12).Radius(7).Center().
				Background(ColorPillSel).Cursor(ui.CursorPointer)
			if syncBtn.Clicked() && !st.AG.IsSyncing {
				go m.Store.SyncAntigravityOfficial(true)
			}
			syncBtn.Children(func() {
				lbl := tr(zh, "立即后台同步", "Sync now")
				if st.AG.IsSyncing {
					lbl = tr(zh, "正在同步…", "Syncing…")
				}
				ui.Text(c, lbl).FontSize(11.5).FontWeight(500).TextColor(ColorText).SingleLine()
			})

			loginBtn := ui.Row(c.Key("btn-ag-login")).Height(28).Padding(0, 12).Radius(7).Center().
				Background(ColorPillBg).Cursor(ui.CursorPointer)
			if loginBtn.Clicked() {
				m.Store.LaunchAgyLogin()
			}
			loginBtn.Children(func() {
				ui.Text(c, tr(zh, "终端登录 agy", "CLI Login")).FontSize(11.5).FontWeight(500).TextColor(ColorText).SingleLine()
			})

			bridgeLbl := tr(zh, "安装 statusLine 桥接", "Install bridge")
			if st.AG.BridgeStatus.IsInstalled() {
				bridgeLbl = tr(zh, "移除 statusLine 桥接", "Remove bridge")
			}
			bridgeBtn := ui.Row(c.Key("btn-ag-bridge")).Height(28).Padding(0, 12).Radius(7).Center().
				Background(ColorPillBg).Cursor(ui.CursorPointer)
			if bridgeBtn.Clicked() {
				if st.AG.BridgeStatus.IsInstalled() {
					go m.Store.UninstallAGBridge()
				} else {
					go m.Store.InstallAGBridge()
				}
			}
			bridgeBtn.Children(func() {
				ui.Text(c, bridgeLbl).FontSize(11.5).TextColor(ColorSecondary).SingleLine()
			})
		})

		if st.AG.Feedback != "" {
			ui.Text(c, st.AG.Feedback).FontSize(11).TextColor(ColorSecondary)
		}
	})

	Hairline(c)

	agCurDay := dayOpts[0]
	if st.Prefs.AGBillingAnchorDay >= 1 && st.Prefs.AGBillingAnchorDay <= 31 {
		agCurDay = dayOpts[st.Prefs.AGBillingAnchorDay]
	}
	agRangeDesc := tr(zh, "设置后按账单日切分 Antigravity 本月周期并显示剩余天数",
		"Aligns Antigravity monthly spend cycle and shows days left")
	if st.AG.LocalReport.MonthPeriod != nil && st.Prefs.AGBillingAnchorDay > 0 {
		agRangeDesc = tr(zh, "当前周期 ", "Current period ") + core.FormatPeriodRange(
			st.AG.LocalReport.MonthPeriod.Start, st.AG.LocalReport.MonthPeriod.End.Add(time.Second), zh)
	}
	settingsRow(c, tr(zh, "Antigravity 每月账单日", "Antigravity billing day"), agRangeDesc, func() {
		sel := agCurDay
		if ui.Select(c.Key("sel-ag-anchor"), &sel, dayOpts).Width(120).Changed() {
			for i, o := range dayOpts {
				if o == sel {
					m.Store.UpdatePrefs(func(p *core.Preferences) { p.AGBillingAnchorDay = i })
					break
				}
			}
		}
	})
}

func (m *SettingsModel) renderMenuBarTab(c *ui.Context, st service.ViewState, zh bool) {
	previewIdx := 0
	if m.menuBarPreviewProvider == core.ProviderAntigravity {
		previewIdx = 1
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
		ui.Text(c, tr(zh, "菜单栏与悬浮条预览", "Menubar & Floating Bar Preview")).FontSize(11.5).FontWeight(500).TextColor(ColorTertiary)
		ui.Spacer(c)
		SegmentedTabs(c, "seg-mb-prov", []string{"Claude", "Antigravity"}, previewIdx, false, func(i int) {
			if i == 1 {
				m.menuBarPreviewProvider = core.ProviderAntigravity
			} else {
				m.menuBarPreviewProvider = core.ProviderClaude
			}
		})
	})

	// Live preview strip matching hero.png / settings.png!
	ui.Row(c).FillWidth().Height(42).Padding(0, 16).Radius(10).Background(ColorPillBg).Center().Gap(12).Children(func() {
		ui.Box(c).Size(12, 3).Radius(1.5).Background(ColorQuaternary)
		if m.menuBarPreviewProvider == core.ProviderAntigravity {
			m.renderAGPreviewPillContent(c, st, zh)
		} else {
			m.renderPreviewPillContent(c, st, zh)
		}
		ui.Box(c).Size(12, 3).Radius(1.5).Background(ColorQuaternary)
		ui.Text(c, time.Now().Format("15:04")).FontSize(12).FontWeight(500).TextColor(ColorSecondary).FontFeatures("tnum")
	})

	settingsRow(c, tr(zh, "桌面 Mini 悬浮条（Windows 专属）", "Floating Mini Bar (Windows)"),
		tr(zh, "还原 macOS 菜单栏常驻多列数值体验。随主面板切换显示 Claude 或 Antigravity。",
			"Compact desktop strip following the active monitor (Claude or Antigravity)."), func() {
			TerracottaSwitch(c, "sw-pill", st.Prefs.ShowFloatingPill, func(on bool) {
				m.Store.UpdatePrefs(func(p *core.Preferences) { p.ShowFloatingPill = on })
				if m.Actions.ToggleFloatingPill != nil {
					m.Actions.ToggleFloatingPill(on)
				}
			})
		})

	Hairline(c)

	settingsRow(c, tr(zh, "悬浮条始终置顶", "Keep floating bar on top"),
		tr(zh, "开启后悬浮条始终显示在所有窗口最前面，关闭后可被其他窗口遮挡。",
			"Keeps the floating bar above all other windows when enabled."), func() {
			TerracottaSwitch(c, "sw-pill-top", st.Prefs.PillAlwaysOnTop, func(on bool) {
				m.Store.UpdatePrefs(func(p *core.Preferences) { p.PillAlwaysOnTop = on })
				if m.Actions.SetPillAlwaysOnTop != nil {
					m.Actions.SetPillAlwaysOnTop(on)
				}
			})
		})

	Hairline(c)

	if m.menuBarPreviewProvider == core.ProviderAntigravity {
		m.renderAGMenuBarSettings(c, st, zh)
		return
	}

	settingsRow(c, tr(zh, "图标", "Icon"), "", func() {
		idx := 0
		if st.Prefs.IconStyle == core.IconStyleLogo {
			idx = 1
		}
		SegmentedTabs(c, "seg-icon", []string{"Clawd", tr(zh, "Claude 标志", "Claude logo")}, idx, false, func(i int) {
			m.Store.UpdatePrefs(func(p *core.Preferences) {
				if i == 0 {
					p.IconStyle = core.IconStyleClawd
				} else {
					p.IconStyle = core.IconStyleLogo
				}
			})
			if m.Actions.SyncTray != nil {
				m.Actions.SyncTray()
			}
		})
	})

	Hairline(c)

	settingsRow(c, tr(zh, "Clawd 动画", "Clawd animations"),
		tr(zh, "眨眼、张望、蹦跳，动作取自 Claude Code。", "Blinks and looks around using official Claude Code poses."), func() {
			TerracottaSwitch(c, "sw-anim", st.Prefs.ClawdAnimation, func(on bool) {
				m.Store.UpdatePrefs(func(p *core.Preferences) { p.ClawdAnimation = on })
			})
		})

	Hairline(c)

	// Multi-select metric strip matching hero.png "显示数值" — uses live values (v1.6.3)
	c.After(time.Second)
	now := time.Now()
	money := core.NewMoneyFormatWithRates(st.Prefs.Currency, st.FXRates)

	var eff *core.OfficialUsage
	if st.OfficialUsage != nil {
		e := st.OfficialUsage.EffectiveAt(now)
		eff = &e
	}

	v5h, c5h := "42%", ColorLive
	if eff != nil && eff.FiveHour != nil {
		v5h = fmt.Sprintf("%d%%", eff.FiveHour.Percent())
		c5h = LevelColor(eff.FiveHour.Fraction())
	}
	v7d, c7d := "68%", ColorLive
	if eff != nil && eff.SevenDay != nil {
		v7d = fmt.Sprintf("%d%%", eff.SevenDay.Percent())
		c7d = LevelColor(eff.SevenDay.Fraction())
	}
	scopedLabel, vScoped, cScoped := "Sonnet", "12%", ColorSecondary
	if eff != nil && len(eff.Scoped) > 0 {
		sc := eff.Scoped[0]
		scopedLabel = sc.ModelName
		vScoped = fmt.Sprintf("%d%%", sc.Limit.Percent())
		cScoped = LevelColor(sc.Limit.Fraction())
	}
	vToday := money.Compact(12)
	vWeekCost := money.Compact(86)
	vMonthCost := money.Compact(1234)
	if st.Snapshot != nil {
		if td := st.Snapshot.Today(); td != nil {
			vToday = money.Compact(td.Cost)
		} else {
			vToday = money.Compact(0)
		}
		if st.Snapshot.Week != nil {
			vWeekCost = money.Compact(st.Snapshot.Week.Cost)
		}
		vMonthCost = money.Compact(st.Snapshot.Billing.Cost)
	}
	// Output speed chip always reflects live 5m rate ("–" when no reply in last 5m).
	vSpeed := "–"
	nowSec := float64(now.Unix()) + float64(now.Nanosecond())/1e9
	models := st.SpeedLog.Models(nowSec)
	if active := st.ActiveSpeedModel(models); active != nil {
		if recent := core.ComputeSpeedRate(active.Requests, nowSec-core.SpeedRecentWindow, nowSec); recent != nil {
			vSpeed = fmt.Sprintf("%d", int(math.Round(recent.TokensPerSecond())))
		}
	}

	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Text(c, tr(zh, "显示数值", "Visible metrics")).FontSize(13).FontWeight(500).TextColor(ColorText)
		ui.Text(c, tr(zh, "亮起的会显示在悬浮条和预览栏上，点一下切换，可以多选。", "Click items to toggle them in the floating bar.")).
			FontSize(11.5).TextColor(ColorTertiary)

		type metricChip struct {
			key   string
			label string
			val   string
			col   ui.Color
			on    bool
			set   func(*core.Preferences, bool)
		}
		items := []metricChip{
			{"m5h", tr(zh, "5小时", "5h"), v5h, c5h, st.Prefs.PillShowFiveHour, func(p *core.Preferences, v bool) { p.PillShowFiveHour = v }},
			{"m7d", tr(zh, "本周", "Week"), v7d, c7d, st.Prefs.PillShowWeek, func(p *core.Preferences, v bool) { p.PillShowWeek = v }},
			{"msc", scopedLabel, vScoped, cScoped, st.Prefs.PillShowScoped, func(p *core.Preferences, v bool) { p.PillShowScoped = v }},
			{"mtd", tr(zh, "今日", "Today"), vToday, ColorText, st.Prefs.PillShowTodayCost, func(p *core.Preferences, v bool) { p.PillShowTodayCost = v }},
			{"mwk", tr(zh, "本周费", "Wk $"), vWeekCost, ColorText, st.Prefs.PillShowWeekCost, func(p *core.Preferences, v bool) { p.PillShowWeekCost = v }},
			{"mmo", tr(zh, "本月费", "Mo $"), vMonthCost, ColorText, st.Prefs.PillShowMonthCost, func(p *core.Preferences, v bool) { p.PillShowMonthCost = v }},
			{"msp", tr(zh, "速度", "Speed"), vSpeed, ColorText, st.Prefs.PillShowSpeed, func(p *core.Preferences, v bool) { p.PillShowSpeed = v }},
		}
		ui.Row(c).Padding(4).Gap(4).Radius(9).Background(ColorPillBg).Children(func() {
			for _, it := range items {
				item := it
				box := ui.Column(c.Key(item.key)).Padding(4, 9).Radius(7).Center().Gap(1).Cursor(ui.CursorPointer)
				if item.on {
					box.Background(ColorPillSel)
				} else if box.Hovered() {
					box.Background(ColorHover)
				}
				if box.Clicked() {
					m.Store.UpdatePrefs(func(p *core.Preferences) { item.set(p, !item.on) })
					if m.Actions.SyncPillSize != nil {
						m.Actions.SyncPillSize()
					}
				}
				op := float32(0.35)
				if item.on {
					op = 1
				}
				box.Opacity(op).Children(func() {
					ui.Text(c, item.label).FontSize(9.5).TextColor(ColorSecondary).SingleLine()
					ui.Text(c, item.val).FontSize(11.5).FontWeight(600).TextColor(item.col).FontFeatures("tnum").SingleLine()
				})
			}
		})
	})

	Hairline(c)

	settingsRow(c, tr(zh, "出口安全", "Exit guard"),
		tr(zh, "Clawd 举着出口盾牌。官方请求只走 IPv4。IPv6 直连中国大陆、香港或澳门时发出警告。",
			"Blocks official calls in CN/HK/MO and warns on direct IPv6 leaks."), func() {
			TerracottaSwitch(c, "sw-exit", st.Prefs.ExitGuard, func(on bool) {
				m.Store.UpdatePrefs(func(p *core.Preferences) { p.ExitGuard = on })
				if m.Actions.SyncTray != nil {
					m.Actions.SyncTray()
				}
			})
		})

	settingsGroupTitle(c, tr(zh, "提醒", "Alerts"))

	settingsRow(c, tr(zh, "用量预警通知", "Usage warning alerts"),
		tr(zh, "超过预警线、以及达到 95% 时各发一次 Windows 系统通知", "Sends a Windows Toast alert at threshold and 95%."), func() {
			TerracottaSwitch(c, "sw-alert", st.Prefs.AlertEnabled, func(on bool) {
				m.Store.UpdatePrefs(func(p *core.Preferences) { p.AlertEnabled = on })
			})
		})

	thresh := float64(st.Prefs.AlertThresholdPct)
	settingsRow(c, tr(zh, "预警线", "Alert threshold"), "", func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			if ui.Slider(c.Key("sl-thresh"), &thresh, 50, 95).Width(130).Changed() {
				m.Store.UpdatePrefs(func(p *core.Preferences) { p.AlertThresholdPct = int(thresh) })
			}
			ui.Text(c, fmt.Sprintf("%d%%", st.Prefs.AlertThresholdPct)).Width(36).TextAlign(ui.End).
				FontSize(12).FontWeight(500).TextColor(ColorSecondary).FontFeatures("tnum")
		})
	})
}

func (m *SettingsModel) renderPreviewPillContent(c *ui.Context, st service.ViewState, zh bool) {
	col := ColorLive
	if st.OfficialUsage != nil && st.OfficialUsage.FiveHour != nil {
		col = LevelColor(st.OfficialUsage.FiveHour.Fraction())
	}
	if st.Prefs.IconStyle == core.IconStyleLogo {
		ClaudeLogo(c, 15, col)
	} else {
		shield := st.Shield
		if shield == platform.ShieldNone && st.Prefs.ExitGuard {
			shield = platform.ShieldSafe
		}
		ClawdMascot(c, 18, col, "idle", shield)
	}
	addMetric := func(top, bot string, vc ui.Color) {
		ui.Column(c).Center().Gap(0).Children(func() {
			ui.Text(c, top).FontSize(8.5).TextColor(ColorTertiary).SingleLine()
			ui.Text(c, bot).FontSize(11).FontWeight(600).TextColor(vc).FontFeatures("tnum").SingleLine()
		})
	}
	now := time.Now()
	money := core.NewMoneyFormatWithRates(st.Prefs.Currency, st.FXRates)
	var eff *core.OfficialUsage
	if st.OfficialUsage != nil {
		e := st.OfficialUsage.EffectiveAt(now)
		eff = &e
	}
	if st.Prefs.PillShowFiveHour {
		v, vc := "–", ColorSecondary
		if eff != nil && eff.FiveHour != nil {
			v = fmt.Sprintf("%d%%", eff.FiveHour.Percent())
			vc = LevelColor(eff.FiveHour.Fraction())
		}
		addMetric(tr(zh, "5小时", "5h"), v, vc)
	}
	if st.Prefs.PillShowWeek {
		v, vc := "–", ColorSecondary
		if eff != nil && eff.SevenDay != nil {
			v = fmt.Sprintf("%d%%", eff.SevenDay.Percent())
			vc = LevelColor(eff.SevenDay.Fraction())
		}
		addMetric(tr(zh, "本周", "Week"), v, vc)
	}
	if st.Prefs.PillShowScoped {
		lbl, v, vc := "Sonnet", "–", ColorSecondary
		if eff != nil && len(eff.Scoped) > 0 {
			sc := eff.Scoped[0]
			lbl = sc.ModelName
			v = fmt.Sprintf("%d%%", sc.Limit.Percent())
			vc = LevelColor(sc.Limit.Fraction())
		}
		addMetric(lbl, v, vc)
	}
	if st.Prefs.PillShowTodayCost {
		cost := 0.0
		if st.Snapshot != nil && st.Snapshot.Today() != nil {
			cost = st.Snapshot.Today().Cost
		}
		addMetric(tr(zh, "今日", "Today"), money.Compact(cost), ColorText)
	}
	if st.Prefs.PillShowWeekCost {
		v := "–"
		if st.Snapshot != nil && st.Snapshot.Week != nil {
			v = money.Compact(st.Snapshot.Week.Cost)
		}
		addMetric(tr(zh, "本周费", "Wk $"), v, ColorText)
	}
	if st.Prefs.PillShowMonthCost {
		v := "–"
		if st.Snapshot != nil {
			v = money.Compact(st.Snapshot.Billing.Cost)
		}
		addMetric(tr(zh, "本月费", "Mo $"), v, ColorText)
	}
	if st.Prefs.PillShowSpeed {
		v := "–"
		nowSec := float64(now.Unix()) + float64(now.Nanosecond())/1e9
		models := st.SpeedLog.Models(nowSec)
		if active := st.ActiveSpeedModel(models); active != nil {
			if recent := core.ComputeSpeedRate(active.Requests, nowSec-core.SpeedRecentWindow, nowSec); recent != nil {
				v = fmt.Sprintf("%d", int(math.Round(recent.TokensPerSecond())))
			}
		}
		addMetric(tr(zh, "速度", "Speed"), v, ColorText)
	}
}

func (m *SettingsModel) renderAGPreviewPillContent(c *ui.Context, st service.ViewState, zh bool) {
	AntigravityLogoAnimated(c, "ag-preview-logo", 15, st.Prefs.AGLogoAnimation && st.AG.Execution.IsActiveAt(time.Now()))
	money := core.NewMoneyFormatWithRates(st.Prefs.Currency, st.FXRates)
	cols := buildAGPillColumns(st, money, zh)
	if len(cols) == 1 {
		ui.Text(c, cols[0].val).FontSize(12).FontWeight(600).TextColor(cols[0].col).FontFeatures("tnum").SingleLine()
		return
	}
	for _, col := range cols {
		ui.Column(c).Center().Gap(0).Children(func() {
			ui.Text(c, col.top).FontSize(8.5).TextColor(ColorTertiary).SingleLine()
			ui.Text(c, col.val).FontSize(11).FontWeight(600).TextColor(col.col).FontFeatures("tnum").SingleLine()
		})
	}
}

func (m *SettingsModel) renderAGMenuBarSettings(c *ui.Context, st service.ViewState, zh bool) {
	money := core.NewMoneyFormatWithRates(st.Prefs.Currency, st.FXRates)
	off := st.AG.OfficialReport
	local := st.AG.LocalReport

	v5h := "--"
	c5h := ColorAGSafeGreen
	if off.PrimaryFiveHour != nil {
		v5h = fmt.Sprintf("%d%%", off.PrimaryFiveHour.UsedPercentInt())
		c5h = AGLevelColor(off.PrimaryFiveHour.UsedFraction, off.PrimaryFiveHour.IsOverPace())
	}
	vWk := "--"
	cWk := ColorAGSafeGreen
	if off.PrimaryWeekly != nil {
		vWk = fmt.Sprintf("%d%%", off.PrimaryWeekly.UsedPercentInt())
		cWk = AGLevelColor(off.PrimaryWeekly.UsedFraction, off.PrimaryWeekly.IsOverPace())
	}
	vCache := "--"
	if local.Today.TotalInputTokens() > 0 {
		vCache = core.FormatPercent(local.Today.CacheHitRate(), 0)
	}
	vToday := money.Compact(local.Today.EstimatedCostUSD)
	vCtx := "--"
	if usedPct, _, _, ok := core.ResolveAGContextWindow(off, local); ok {
		vCtx = fmt.Sprintf("%d%%", int(math.Round(usedPct)))
	}

	settingsRow(c, tr(zh, "执行时图标扫光", "Logo sheen while active"),
		tr(zh, "Antigravity 执行时，悬浮条星标显示柔和斜向扫光动画。", "Subtle diagonal sheen sweeps across the star while Antigravity is running."), func() {
			AccentSwitch(c, "sw-ag-anim", st.Prefs.AGLogoAnimation, ColorAGAccent, func(on bool) {
				m.Store.UpdatePrefs(func(p *core.Preferences) { p.AGLogoAnimation = on })
			})
		})

	Hairline(c)

	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Text(c, tr(zh, "Antigravity 显示项", "Antigravity visible metrics")).FontSize(13).FontWeight(500).TextColor(ColorText)
		ui.Text(c, tr(zh, "亮起的数值会显示在 Antigravity 悬浮条与预览栏中，点击即可开启或关闭。",
			"Lit values appear in the Antigravity floating bar. Click to toggle.")).
			FontSize(11.5).TextColor(ColorTertiary)

		type agChip struct {
			key   string
			label string
			val   string
			col   ui.Color
			on    bool
			set   func(*core.Preferences, bool)
		}
		items := []agChip{
			{"ag-m5h", tr(zh, "5小时", "5H"), v5h, c5h, st.Prefs.AGPillShowFiveHour, func(p *core.Preferences, v bool) { p.AGPillShowFiveHour = v }},
			{"ag-mwk", tr(zh, "本周", "WEEK"), vWk, cWk, st.Prefs.AGPillShowWeek, func(p *core.Preferences, v bool) { p.AGPillShowWeek = v }},
			{"ag-mca", tr(zh, "缓存", "CACHE"), vCache, ColorAGAccent, st.Prefs.AGPillShowCache, func(p *core.Preferences, v bool) { p.AGPillShowCache = v }},
			{"ag-mtd", tr(zh, "今日", "TODAY"), vToday, ColorText, st.Prefs.AGPillShowTodayCost, func(p *core.Preferences, v bool) { p.AGPillShowTodayCost = v }},
			{"ag-mctx", tr(zh, "上下文", "CTX"), vCtx, ColorWarning, st.Prefs.AGPillShowContext, func(p *core.Preferences, v bool) { p.AGPillShowContext = v }},
		}
		ui.Row(c).Padding(4).Gap(4).Radius(9).Background(ColorPillBg).Children(func() {
			for _, it := range items {
				item := it
				box := ui.Column(c.Key(item.key)).Grow(1).Padding(4, 9).Radius(7).Center().Gap(1).Cursor(ui.CursorPointer)
				if item.on {
					box.Background(ColorPillSel)
				} else if box.Hovered() {
					box.Background(ColorHover)
				}
				if box.Clicked() {
					m.Store.UpdatePrefs(func(p *core.Preferences) { item.set(p, !item.on) })
					if m.Actions.SyncPillSize != nil {
						m.Actions.SyncPillSize()
					}
				}
				op := float32(0.35)
				if item.on {
					op = 1
				}
				box.Opacity(op).Children(func() {
					ui.Text(c, item.label).FontSize(9.5).TextColor(ColorSecondary).SingleLine()
					ui.Text(c, item.val).FontSize(11.5).FontWeight(600).TextColor(item.col).FontFeatures("tnum").SingleLine()
				})
			}
		})
	})

	Hairline(c)

	settingsGroupTitle(c, tr(zh, "Antigravity 提醒", "Antigravity alerts"))
	settingsRow(c, tr(zh, "额度预警通知", "Usage alerts"),
		tr(zh, "超过安全配速线或达到 95% 时发送 Windows 系统通知", "Notify once past safe line and again at 95%."), func() {
			AccentSwitch(c, "sw-ag-alert", st.Prefs.AGAlertEnabled, ColorAGAccent, func(on bool) {
				m.Store.UpdatePrefs(func(p *core.Preferences) { p.AGAlertEnabled = on })
			})
		})
}

func (m *SettingsModel) renderPanelTab(c *ui.Context, st service.ViewState, zh bool) {
	settingsGroupTitle(c, tr(zh, "内容", "Content"))

	exitIdx := 0
	if st.Prefs.ExitDisplay == core.ShowOnWarn {
		exitIdx = 1
	}
	settingsRow(c, tr(zh, "出口", "Exit"), tr(zh, "出口所在的国家或地区和 IP，指针停上去显示完整地址", "Country/region and masked IP; hover reveals full IP."), func() {
		SegmentedTabs(c, "seg-exit", []string{tr(zh, "始终", "Always"), tr(zh, "异常时", "On issue")}, exitIdx, false, func(i int) {
			m.Store.UpdatePrefs(func(p *core.Preferences) {
				if i == 0 {
					p.ExitDisplay = core.ShowAlways
				} else {
					p.ExitDisplay = core.ShowOnWarn
				}
			})
		})
	})

	Hairline(c)

	settingsRow(c, tr(zh, "进程", "Processes"), tr(zh, "正在运行的 Claude 会话，展开后可以强制退出", "Running Claude sessions with hover force-quit."), func() {
		TerracottaSwitch(c, "sw-procs", st.Prefs.ShowProcesses, func(on bool) {
			m.Store.UpdatePrefs(func(p *core.Preferences) { p.ShowProcesses = on })
		})
	})

	Hairline(c)

	modeLabels := []string{tr(zh, "详细", "Detailed"), tr(zh, "简洁", "Compact"), tr(zh, "不显示", "Hidden")}
	modeIdx := func(d core.DisplayMode) int {
		switch d {
		case core.DisplayCompact:
			return 1
		case core.DisplayHidden:
			return 2
		default:
			return 0
		}
	}
	idxMode := func(i int) core.DisplayMode {
		switch i {
		case 1:
			return core.DisplayCompact
		case 2:
			return core.DisplayHidden
		default:
			return core.DisplayDetailed
		}
	}

	settingsRow(c, tr(zh, "用量限额", "Limits"), tr(zh, "每项一条进度条和安全线，下面写重置时间", "Progress bar, safe line, and reset time per limit."), func() {
		SegmentedTabs(c, "seg-lim", modeLabels, modeIdx(st.Prefs.LimitsDisplay), false, func(i int) {
			m.Store.UpdatePrefs(func(p *core.Preferences) { p.LimitsDisplay = idxMode(i) })
		})
	})

	Hairline(c)

	settingsRow(c, tr(zh, "输出速度", "Output speed"), tr(zh, "近 5 分钟的速度和最近 1 小时的折线", "5-minute speed and 1-hour trend/scatter chart."), func() {
		SegmentedTabs(c, "seg-spd", modeLabels, modeIdx(st.Prefs.SpeedDisplay), false, func(i int) {
			m.Store.UpdatePrefs(func(p *core.Preferences) { p.SpeedDisplay = idxMode(i) })
		})
	})

	Hairline(c)

	settingsRow(c, tr(zh, "本期消耗", "Spend"), tr(zh, "金额、预计、Token 构成、缓存命中率和各模型花费", "Spend, estimate, token breakdown, cache hit rate, models."), func() {
		SegmentedTabs(c, "seg-spend", modeLabels, modeIdx(st.Prefs.SpendDisplay), false, func(i int) {
			m.Store.UpdatePrefs(func(p *core.Preferences) { p.SpendDisplay = idxMode(i) })
		})
	})

	ui.Text(c, tr(zh, "在面板里点区块的标题可以收起或展开，收起后标题右侧显示摘要。", "Click any section header in the panel to collapse or expand it.")).
		FontSize(11).TextColor(ColorTertiary)

	settingsGroupTitle(c, tr(zh, "重置时间", "Reset time"))

	fmtIdx := 0
	if st.Prefs.FullDateReset {
		fmtIdx = 1
	}
	settingsRow(c, tr(zh, "时间格式", "Format"), tr(zh, "面板里各额度的重置时间，按系统当前时区", "Displayed in local timezone."), func() {
		SegmentedTabs(c, "seg-resetfmt", []string{tr(zh, "周六 22:00", "Sat 22:00"), tr(zh, "2026年10月31日 22:00", "Oct 31, 2026 22:00")}, fmtIdx, false, func(i int) {
			m.Store.UpdatePrefs(func(p *core.Preferences) { p.FullDateReset = i == 1 })
		})
	})

	Hairline(c)

	settingsRow(c, tr(zh, "显示倒计时", "Show countdown"), tr(zh, "在重置时间右侧显示还剩多久，精确到秒，每秒刷新", "Shows live HH:MM:SS countdown beside reset time."), func() {
		TerracottaSwitch(c, "sw-cnt", st.Prefs.ShowResetCountdown, func(on bool) {
			m.Store.UpdatePrefs(func(p *core.Preferences) { p.ShowResetCountdown = on })
		})
	})

	settingsGroupTitle(c, tr(zh, "货币", "Currency"))

	curOpts := make([]string, len(core.AllCurrencies))
	for i, cu := range core.AllCurrencies {
		curOpts[i] = string(cu)
	}
	curSel := string(st.Prefs.Currency)
	settingsRow(c, tr(zh, "显示货币", "Display currency"), tr(zh, "实时汇率，也可以在主面板直接点击金额循环切换", "Live exchange rates; or click the spend amount directly in the panel."), func() {
		if ui.Select(c.Key("sel-cur"), &curSel, curOpts).Width(100).Changed() {
			m.Store.UpdatePrefs(func(p *core.Preferences) { p.Currency = core.CurrencyUnit(curSel) })
		}
	})

	settingsGroupTitle(c, "Antigravity")

	settingsRow(c, tr(zh, "Antigravity 活跃进程", "Antigravity processes"),
		tr(zh, "控制 Antigravity 面板顶部活跃进程行的展示方式", "Controls the active processes row in Antigravity view."), func() {
			SegmentedTabs(c, "seg-ag-proc", modeLabels, modeIdx(st.Prefs.AGProcessesDisplay), false, func(i int) {
				m.Store.UpdatePrefs(func(p *core.Preferences) { p.AGProcessesDisplay = idxMode(i) })
			})
		})

	Hairline(c)

	settingsRow(c, tr(zh, "Antigravity 近似输出速度（估算）", "Antigravity estimated output speed"),
		tr(zh, "基于本地对话日志间隔近似估算，非真实生成速度，可随时关闭",
			"Approximated from local transcript intervals, not real token timing."), func() {
			AccentSwitch(c, "sw-ag-spd", st.Prefs.AGShowEstimatedSpeed, ColorAGAccent, func(on bool) {
				m.Store.UpdatePrefs(func(p *core.Preferences) { p.AGShowEstimatedSpeed = on })
			})
		})
}

func (m *SettingsModel) renderDataTab(c *ui.Context, st service.ViewState, zh bool) {
	settingsGroupTitle(c, tr(zh, ".claude 目录", ".claude directory"))

	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				ui.Text(c, tr(zh, "自定义 .claude 目录位置", "Custom .claude directory")).FontSize(13).FontWeight(500).TextColor(ColorText)
				ui.Text(c, tr(zh, "留空使用默认位置。修改后将自动从该目录读取登录凭据（.credentials.json）、缓存配置与会话日志（projects），支持本地或 WSL 路径。",
					"Leave empty for default. Used to locate .credentials.json, .claude.json cache, and projects logs (supports local or WSL paths).")).
					FontSize(11.5).TextColor(ColorTertiary)
			})
		})

		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			placeholder := platform.DefaultClaudeConfigDir()
			inp := ui.TextInput(c.Key("in-claude-dir"), &m.claudeDirDraft).
				Grow(1).Placeholder(placeholder).FontSize(12)
			if inp.Submitted() {
				val := strings.TrimSpace(m.claudeDirDraft)
				m.Store.UpdatePrefs(func(p *core.Preferences) { p.ClaudeConfigDir = val })
			}

			applyBtn := ui.Row(c.Key("btn-apply-claude-dir")).Height(28).Padding(0, 12).Radius(7).Center().
				Background(ColorAccent.Alpha(0.22)).Cursor(ui.CursorPointer)
			if applyBtn.Clicked() {
				val := strings.TrimSpace(m.claudeDirDraft)
				m.Store.UpdatePrefs(func(p *core.Preferences) { p.ClaudeConfigDir = val })
			}
			applyBtn.Children(func() {
				ui.Text(c, tr(zh, "应用", "Apply")).FontSize(12).FontWeight(500).TextColor(ColorAccent).SingleLine()
			})

			if m.Actions.ChooseClaudeDir != nil {
				pickBtn := ui.Row(c.Key("btn-pick-claude-dir")).Height(28).Padding(0, 12).Radius(7).Center().
					Background(ColorPillSel).Cursor(ui.CursorPointer)
				if pickBtn.Clicked() {
					m.Actions.ChooseClaudeDir()
				}
				pickBtn.Children(func() {
					ui.Text(c, tr(zh, "选择目录…", "Browse…")).FontSize(12).TextColor(ColorText).SingleLine()
				})
			}

			if strings.TrimSpace(st.Prefs.ClaudeConfigDir) != "" {
				resetBtn := ui.Row(c.Key("btn-reset-claude-dir")).Height(28).Padding(0, 10).Radius(7).Center().
					Background(ColorPillBg).Cursor(ui.CursorPointer)
				if resetBtn.Clicked() {
					m.claudeDirDraft = ""
					m.Store.UpdatePrefs(func(p *core.Preferences) { p.ClaudeConfigDir = "" })
				}
				resetBtn.Children(func() {
					ui.Text(c, tr(zh, "默认", "Reset")).FontSize(11.5).TextColor(ColorSecondary).SingleLine()
				})
			}
		})

		// Live detection status of current effective .claude directory
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr(zh, "当前生效：", "Active: ")+st.ClaudeConfigDir).
				FontSize(11).TextColor(ColorTertiary).SingleLine().Grow(1)
			if st.HasCredentialsFile {
				Chip(c, tr(zh, "已找到凭据", "Credentials OK"), ColorLive)
			} else {
				Chip(c, tr(zh, "无凭据文件", "No credentials"), ColorTertiary)
			}
			if st.HasProjectsDir {
				Chip(c, tr(zh, "已找到日志", "Logs OK"), ColorLive)
			} else {
				Chip(c, tr(zh, "无 projects", "No projects"), ColorTertiary)
			}
		})
	})

	Hairline(c)

	settingsGroupTitle(c, tr(zh, "额外会话日志目录", "Additional session log folder"))

	roots := strings.Join(st.DataRoots, "\n")
	settingsRow(c, tr(zh, "当前扫描的日志目录", "Scanned log folders"), roots, func() {
		ui.Row(c).Gap(6).Children(func() {
			if m.Actions.ChooseDir != nil {
				b := ui.Row(c.Key("btn-dir")).Height(28).Padding(0, 12).Radius(7).Center().Background(ColorPillSel).Cursor(ui.CursorPointer)
				if b.Clicked() {
					m.Actions.ChooseDir()
				}
				b.Children(func() {
					ui.Text(c, tr(zh, "选择目录…", "Choose…")).FontSize(12).TextColor(ColorText).SingleLine()
				})
			}
			if strings.TrimSpace(st.Prefs.CustomDataDir) != "" {
				clr := ui.Row(c.Key("btn-clr-dir")).Height(28).Padding(0, 10).Radius(7).Center().Background(ColorPillBg).Cursor(ui.CursorPointer)
				if clr.Clicked() {
					m.Store.UpdatePrefs(func(p *core.Preferences) { p.CustomDataDir = "" })
				}
				clr.Children(func() {
					ui.Text(c, tr(zh, "清除", "Clear")).FontSize(11.5).TextColor(ColorSecondary).SingleLine()
				})
			}
		})
	})

	Hairline(c)

	recCount := 0
	if st.Snapshot != nil {
		recCount = st.Snapshot.TotalRecords
	}
	idxDesc := tr(zh, fmt.Sprintf("已索引 %s 条记录 · 缓存 %s", core.FormatGrouped(int64(recCount)), platform.FormatBytes(uint64(st.IndexSize))),
		fmt.Sprintf("%s records indexed · Cache %s", core.FormatGrouped(int64(recCount)), platform.FormatBytes(uint64(st.IndexSize))))
	settingsRow(c, tr(zh, "本地索引缓存", "Local index cache"), idxDesc, func() {
		b := ui.Row(c.Key("btn-reidx")).Height(28).Padding(0, 12).Radius(7).Center().Background(ColorPillSel).Cursor(ui.CursorPointer)
		if b.Clicked() {
			m.Store.RebuildIndex()
		}
		b.Children(func() {
			ui.Text(c, tr(zh, "重新索引", "Rebuild index")).FontSize(12).TextColor(ColorText).SingleLine()
		})
	})

	Hairline(c)

	settingsRow(c, tr(zh, "缓存目录", "App data directory"), platform.AppDataDir(), func() {
		b := ui.Row(c.Key("btn-open-data")).Height(28).Padding(0, 12).Radius(7).Center().Background(ColorPillBg).Cursor(ui.CursorPointer)
		if b.Clicked() {
			_ = mygo.Shell.OpenPath(platform.AppDataDir())
		}
		b.Children(func() {
			ui.Text(c, tr(zh, "打开文件夹", "Open folder")).FontSize(12).TextColor(ColorSecondary).SingleLine()
		})
	})

	Hairline(c)

	settingsGroupTitle(c, tr(zh, "Antigravity 数据源", "Antigravity data"))

	{
		all := st.AG.LocalReport.AllTime
		money := core.NewMoneyFormatWithRates(st.Prefs.Currency, st.FXRates)
		sinceText := ""
		if st.AG.LocalReport.EarliestRecordDate != nil {
			dt := st.AG.LocalReport.EarliestRecordDate.Local()
			if zh {
				sinceText = "自 " + dt.Format("2006年1月2日") + " 起"
			} else {
				sinceText = "Since " + dt.Format("Jan 2, 2006")
			}
		}
		statCol := func(label, val string) {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, label).FontSize(10).TextColor(ColorTertiary).SingleLine()
				ui.Text(c, val).FontSize(12).FontWeight(500).TextColor(ColorText).FontFeatures("tnum").SingleLine()
			})
		}

		ui.Column(c).FillWidth().Padding(14).Gap(10).Radius(10).Margin(0, 0, 8, 0).
			Background(ColorPillBg).Border(1, ColorSurfaceBorder).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Baseline).Children(func() {
				ui.Text(c, tr(zh, "累计 API 等效用量", "All-time API-equivalent usage")).
					FontSize(12.5).FontWeight(500).TextColor(ColorText).SingleLine()
				ui.Spacer(c)
				if sinceText != "" {
					ui.Text(c, sinceText).FontSize(10.5).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
				}
			})

			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Baseline).Children(func() {
				ui.Text(c, money.Format(all.EstimatedCostUSD)).
					FontSize(22).FontWeight(600).TextColor(ColorText).FontFeatures("tnum").SingleLine()
				if all.CacheSavingsUSD > 0 {
					ui.Text(c, tr(zh, "缓存累计省 "+money.Format(all.CacheSavingsUSD), "Saved "+money.Format(all.CacheSavingsUSD))).
						FontSize(11).TextColor(ColorAGSafeGreen).FontFeatures("tnum").SingleLine()
				}
			})

			ui.Row(c).FillWidth().Gap(12).Children(func() {
				statCol(tr(zh, "总 Tokens", "Tokens"), core.FormatMagnitude(all.TotalTokens(), zh))
				statCol(tr(zh, "请求", "Requests"), core.FormatGrouped(int64(all.RequestCount)))
				statCol(tr(zh, "会话", "Sessions"), fmt.Sprintf("%d", st.AG.LocalReport.ConversationCount))
				statCol(tr(zh, "缓存命中", "Cache hit"), fmt.Sprintf("%.1f%%", all.CacheHitRate()*100))
			})

			Hairline(c)

			ui.Row(c).FillWidth().Gap(12).Children(func() {
				statCol(tr(zh, "新增输入", "Input"), core.FormatMagnitude(all.UncachedInputTokens, zh))
				statCol(tr(zh, "输出", "Output"), core.FormatMagnitude(all.OutputTokens, zh))
				statCol(tr(zh, "缓存读取", "Cache read"), core.FormatMagnitude(all.CacheReadTokens, zh))
			})
		})
	}

	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Text(c, tr(zh, "自定义 Antigravity brain 目录", "Custom Antigravity brain directory")).FontSize(13).FontWeight(500).TextColor(ColorText)
		ui.Text(c, tr(zh, "留空使用默认位置（~/.gemini/antigravity/brain），读取其中会话的 transcript.jsonl。",
			"Leave empty for default (~/.gemini/antigravity/brain) to scan transcript.jsonl files.")).
			FontSize(11.5).TextColor(ColorTertiary)

		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			inp := ui.TextInput(c.Key("in-ag-brain"), &m.agBrainDraft).
				Grow(1).Placeholder(platform.DefaultAGBrainDir()).FontSize(12)
			if inp.Submitted() {
				val := strings.TrimSpace(m.agBrainDraft)
				m.Store.UpdatePrefs(func(p *core.Preferences) { p.AGBrainDir = val })
			}
			applyBtn := ui.Row(c.Key("btn-apply-ag-brain")).Height(28).Padding(0, 12).Radius(7).Center().
				Background(ColorAGAccent.Alpha(0.22)).Cursor(ui.CursorPointer)
			if applyBtn.Clicked() {
				val := strings.TrimSpace(m.agBrainDraft)
				m.Store.UpdatePrefs(func(p *core.Preferences) { p.AGBrainDir = val })
			}
			applyBtn.Children(func() {
				ui.Text(c, tr(zh, "应用", "Apply")).FontSize(12).FontWeight(500).TextColor(ColorAGAccent).SingleLine()
			})
			if m.Actions.ChooseAGBrainDir != nil {
				pickBtn := ui.Row(c.Key("btn-pick-ag-brain")).Height(28).Padding(0, 12).Radius(7).Center().
					Background(ColorPillSel).Cursor(ui.CursorPointer)
				if pickBtn.Clicked() {
					m.Actions.ChooseAGBrainDir()
				}
				pickBtn.Children(func() {
					ui.Text(c, tr(zh, "选择目录…", "Browse…")).FontSize(12).TextColor(ColorText).SingleLine()
				})
			}
			if strings.TrimSpace(st.Prefs.AGBrainDir) != "" {
				resetBtn := ui.Row(c.Key("btn-reset-ag-brain")).Height(28).Padding(0, 10).Radius(7).Center().
					Background(ColorPillBg).Cursor(ui.CursorPointer)
				if resetBtn.Clicked() {
					m.agBrainDraft = ""
					m.Store.UpdatePrefs(func(p *core.Preferences) { p.AGBrainDir = "" })
				}
				resetBtn.Children(func() {
					ui.Text(c, tr(zh, "默认", "Reset")).FontSize(11.5).TextColor(ColorSecondary).SingleLine()
				})
			}
		})

		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr(zh, "当前生效：", "Active: ")+st.AG.BrainDir).
				FontSize(11).TextColor(ColorTertiary).SingleLine().Grow(1)
			if st.AG.HasBrainDir {
				Chip(c, tr(zh, "已找到日志", "Logs OK"), ColorLive)
			} else {
				Chip(c, tr(zh, "无 brain 目录", "No brain dir"), ColorTertiary)
			}
		})
	})

	Hairline(c)

	agIdxDesc := tr(zh, fmt.Sprintf("已扫描 %d 个会话 · %s 条记录", st.AG.LocalReport.ConversationCount, core.FormatGrouped(int64(st.AG.LocalReport.TotalRecords))),
		fmt.Sprintf("%d conversations · %s records", st.AG.LocalReport.ConversationCount, core.FormatGrouped(int64(st.AG.LocalReport.TotalRecords))))
	settingsRow(c, tr(zh, "Antigravity 对话索引", "Antigravity log index"), agIdxDesc, func() {
		b := ui.Row(c.Key("btn-ag-reidx")).Height(28).Padding(0, 12).Radius(7).Center().Background(ColorPillSel).Cursor(ui.CursorPointer)
		if b.Clicked() {
			m.Store.RebuildAGIndex()
		}
		b.Children(func() {
			ui.Text(c, tr(zh, "重新扫描", "Rescan")).FontSize(12).TextColor(ColorText).SingleLine()
		})
	})

	Hairline(c)

	settingsRow(c, tr(zh, "Antigravity 官方快照", "Antigravity snapshot file"), st.AG.SnapshotPath, nil)
}

func (m *SettingsModel) renderGeneralTab(c *ui.Context, st service.ViewState, zh bool) {
	settingsGroupTitle(c, tr(zh, "通用", "General"))

	langIdx := 0
	if st.Prefs.Language == core.LangEN {
		langIdx = 1
	}
	settingsRow(c, tr(zh, "语言", "Language"), tr(zh, "切换界面显示语言", "Switch interface language"), func() {
		SegmentedTabs(c, "seg-lang", []string{"简体中文", "English"}, langIdx, false, func(i int) {
			m.Store.UpdatePrefs(func(p *core.Preferences) {
				if i == 0 {
					p.Language = core.LangZH
				} else {
					p.Language = core.LangEN
				}
			})
		})
	})

	Hairline(c)

	settingsRow(c, tr(zh, "登录时自动启动", "Launch at login"),
		tr(zh, "开机登录 Windows 时自动在系统托盘启动", "Automatically start in the system tray at Windows sign-in."), func() {
			TerracottaSwitch(c, "sw-login", m.openAtLogin, func(on bool) {
				if m.Actions.SetOpenAtLogin != nil {
					if err := m.Actions.SetOpenAtLogin(on); err == nil {
						m.openAtLogin = on
					}
				} else {
					m.openAtLogin = on
				}
			})
		})

	Hairline(c)

	settingsRow(c, tr(zh, "到期提醒", "Expiry reminders"),
		tr(zh, "订阅临近到期或扣款失败时发送提醒", "Notify before billing period ends or on payment issues."), func() {
			TerracottaSwitch(c, "sw-exp", st.Prefs.ExpiryNotifications, func(on bool) {
				m.Store.UpdatePrefs(func(p *core.Preferences) { p.ExpiryNotifications = on })
			})
		})

	Hairline(c)

	m.renderUpdateSection(c, st, zh)

	Hairline(c)

	ui.Column(c).FillWidth().Padding(8, 0).Gap(4).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ClaudeLogo(c, 16, ColorAccent)
			AntigravityLogo(c, 16, 0)
			ui.Text(c, "Claude & Antigravity Usage Monitor").FontSize(13).FontWeight(600).TextColor(ColorText)
		})
		ui.Text(c, tr(zh, "基于 egoist/mygo Native UI (Direct3D 11)，整合 Claude 与 Antigravity 双源用量与额度监控。",
			"Windows native dual-provider monitor (Claude & Antigravity) powered by egoist/mygo Direct3D 11 UI.")).
			FontSize(11).TextColor(ColorTertiary)
	})
}

func (m *SettingsModel) renderUpdateSection(c *ui.Context, st service.ViewState, zh bool) {
	upd := st.Update
	curVer := strings.TrimSpace(upd.CurrentVersion)
	if curVer == "" {
		curVer = "dev"
	}

	settingsGroupTitle(c, tr(zh, "软件更新", "Software update"))

	statusDesc := tr(zh, "当前版本："+curVer, "Current version: "+curVer)
	if upd.CheckedAt != nil && upd.Status == service.UpdateIdle && !upd.HasAvailable() {
		statusDesc += " · " + tr(zh, "上次检查 "+core.FormatRelative(*upd.CheckedAt, time.Now(), zh),
			"Checked "+core.FormatRelative(*upd.CheckedAt, time.Now(), zh))
	}
	switch upd.Status {
	case service.UpdateChecking:
		statusDesc = tr(zh, "正在从 GitHub Releases 检查最新版本…", "Checking GitHub Releases for updates…")
	case service.UpdateUpToDate:
		statusDesc = tr(zh, fmt.Sprintf("当前版本 %s 已是最新版本", curVer), fmt.Sprintf("Version %s is up to date", curVer))
	case service.UpdateDownloading:
		statusDesc = tr(zh, fmt.Sprintf("正在下载更新包… %d%%", int(math.Round(upd.Progress*100))),
			fmt.Sprintf("Downloading update… %d%%", int(math.Round(upd.Progress*100))))
	case service.UpdateVerifying:
		statusDesc = tr(zh, "正在校验 SHA256 文件摘要…", "Verifying SHA256 checksum…")
	case service.UpdateInstalling:
		statusDesc = tr(zh, "正在原地替换程序文件…", "Installing update in place…")
	case service.UpdateReadyToRestart:
		statusDesc = tr(zh, "更新已完成，正在重新启动…", "Update installed, relaunching…")
	case service.UpdateError:
		if upd.ErrorMsg != "" {
			statusDesc = upd.ErrorMsg
		}
	default:
		if upd.HasAvailable() && upd.Release != nil {
			statusDesc = tr(zh, fmt.Sprintf("当前版本 %s · 发现新版本 %s", curVer, upd.Release.TagName),
				fmt.Sprintf("Current %s · Update %s available", curVer, upd.Release.TagName))
		}
	}

	busy := upd.Status == service.UpdateChecking || upd.Status == service.UpdateDownloading ||
		upd.Status == service.UpdateVerifying || upd.Status == service.UpdateInstalling

	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
		ui.Column(c).Grow(1).Gap(3).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Text(c, tr(zh, "版本 ", "Version ")+curVer).FontSize(13).FontWeight(500).TextColor(ColorText).SingleLine()
				if strings.EqualFold(curVer, "dev") {
					Chip(c, tr(zh, "开发版", "Dev"), ColorTertiary)
				}
				if upd.HasAvailable() && upd.Release != nil {
					Chip(c, upd.Release.TagName, ColorAGAccent)
				}
			})
			descCol := ColorTertiary
			if upd.Status == service.UpdateError {
				descCol = ColorCritical
			}
			ui.Text(c, statusDesc).FontSize(11.5).TextColor(descCol)
		})

		chkBtn := ui.Row(c.Key("btn-upd-chk")).Height(28).Padding(0, 12).Radius(7).Center().Background(ColorPillSel)
		if !busy {
			chkBtn.Cursor(ui.CursorPointer)
			if chkBtn.Clicked() {
				go m.Store.CheckForUpdates(true)
			}
		}
		chkBtn.Children(func() {
			lbl := tr(zh, "检查更新", "Check for updates")
			if upd.Status == service.UpdateChecking {
				lbl = tr(zh, "检查中…", "Checking…")
			}
			ui.Text(c, lbl).FontSize(12).TextColor(ColorText).SingleLine()
		})
	})

	if upd.HasAvailable() && upd.Release != nil {
		rel := upd.Release
		isInstalling := upd.Status == service.UpdateDownloading || upd.Status == service.UpdateVerifying ||
			upd.Status == service.UpdateInstalling || upd.Status == service.UpdateReadyToRestart

		ui.Column(c.Key("upd-card")).FillWidth().Padding(12).Radius(10).Gap(10).
			Background(ColorHover).Border(1, ColorDivider).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				title := tr(zh, "发现新版本 ", "New version ") + rel.TagName
				ui.Text(c, title).FontSize(13).FontWeight(600).TextColor(ColorText).SingleLine()
				if rel.PublishedAt != nil {
					ui.Text(c, rel.PublishedAt.Local().Format("2006-01-02")).FontSize(11).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
				}
				ui.Spacer(c)
				if strings.TrimSpace(rel.Body) != "" {
					tog := ui.Row(c.Key("upd-notes-tog")).Padding(2, 6).Radius(5).AlignItems(ui.Center).Gap(4).Cursor(ui.CursorPointer)
					if tog.Hovered() {
						tog.Background(ColorHover)
					}
					if tog.Clicked() {
						m.updateNotesExpanded = !m.updateNotesExpanded
					}
					tog.Children(func() {
						ui.Text(c, tr(zh, "更新日志", "Release notes")).FontSize(11).TextColor(ColorSecondary).SingleLine()
						ic := IconChevRt
						if m.updateNotesExpanded {
							ic = IconChevDn
						}
						ui.Icon(c, ic).FontSize(9).TextColor(ColorTertiary)
					})
				}
			})

			if m.updateNotesExpanded && strings.TrimSpace(rel.Body) != "" {
				lines := core.ParseReleaseNotesMarkdown(rel.Body)
				ui.Column(c).FillWidth().Padding(8, 10).Radius(7).Gap(4).Background(ui.RGBA(0, 0, 0, 0.18)).Children(func() {
					for i, ln := range lines {
						switch ln.Kind {
						case core.MDHeading:
							ui.Text(c.Key(fmt.Sprintf("md-h-%d", i)), ln.Text).FontSize(11.5).FontWeight(600).TextColor(ColorText)
						case core.MDBullet:
							ui.Text(c.Key(fmt.Sprintf("md-b-%d", i)), "• "+ln.Text).FontSize(11).TextColor(ColorSecondary)
						default:
							ui.Text(c.Key(fmt.Sprintf("md-p-%d", i)), ln.Text).FontSize(11).TextColor(ColorSecondary)
						}
					}
				})
			}

			if isInstalling {
				ui.Column(c).FillWidth().Gap(4).Children(func() {
					ThinBar(c, "upd-prog", upd.Progress, ColorAGAccent, nil, false, nil)
					if upd.Total > 0 {
						ui.Text(c, fmt.Sprintf("%s / %s", platform.FormatBytes(uint64(max(0, upd.Downloaded))), platform.FormatBytes(uint64(upd.Total)))).
							FontSize(10.5).TextColor(ColorTertiary).FontFeatures("tnum").SingleLine()
					}
				})
			}

			if upd.Status == service.UpdateError && upd.ErrorMsg != "" {
				ui.Text(c, upd.ErrorMsg).FontSize(11).TextColor(ColorCritical)
			}

			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				if !isInstalling {
					instBtn := ui.Row(c.Key("btn-upd-inst")).Height(28).Padding(0, 12).Radius(7).Center().
						Background(ColorAGAccent).Cursor(ui.CursorPointer)
					if instBtn.Clicked() {
						m.Store.InstallAvailableUpdate()
					}
					instBtn.Children(func() {
						ui.Text(c, tr(zh, "立即更新并重启", "Update & restart")).FontSize(12).FontWeight(600).TextColor(ui.Hex("#FFFFFF")).SingleLine()
					})
				}

				openBtn := ui.Row(c.Key("btn-upd-web")).Height(28).Padding(0, 12).Radius(7).Center().
					Background(ColorPillSel).Cursor(ui.CursorPointer)
				if openBtn.Clicked() && m.Actions.OpenURL != nil {
					target := strings.TrimSpace(rel.HTMLURL)
					if target == "" {
						target = core.UpdateReleasesURL
					}
					m.Actions.OpenURL(target)
				}
				openBtn.Children(func() {
					ui.Text(c, tr(zh, "前往下载", "Open Release")).FontSize(12).TextColor(ColorText).SingleLine()
				})

				ui.Spacer(c)

				if !isInstalling && !strings.EqualFold(strings.TrimSpace(st.Prefs.SkippedVersion), strings.TrimSpace(rel.TagName)) {
					skipBtn := ui.Row(c.Key("btn-upd-skip")).Height(28).Padding(0, 10).Radius(7).Center().Cursor(ui.CursorPointer)
					if skipBtn.Hovered() {
						skipBtn.Background(ColorHover)
					}
					if skipBtn.Clicked() {
						m.Store.SkipUpdateVersion(rel.TagName)
					}
					skipBtn.Children(func() {
						ui.Text(c, tr(zh, "忽略此版本", "Skip version")).FontSize(11.5).TextColor(ColorSecondary).SingleLine()
					})
				}
			})
		})
	}
}
