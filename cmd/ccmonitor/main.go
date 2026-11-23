package main

import (
	"fmt"
	"log"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"ccmonitor/internal/core"
	"ccmonitor/internal/platform"
	"ccmonitor/internal/service"
	appui "ccmonitor/internal/ui"
)

type application struct {
	store    *service.Store
	tray     *mygo.Tray
	panel    *mygo.Window
	settings *mygo.Window
	pill     *mygo.Window

	popoverModel  *appui.PopoverModel
	settingsModel *appui.SettingsModel
	pillModel     *appui.FloatingPillModel

	pillWidth     int
	pillReady     bool
	pillSaveTimer *time.Timer

	openOnStart bool
	demoMode    bool
	quitting    bool
}

func (a *application) invalidateAll() {
	mygo.RunOnMain(func() {
		if a.panel != nil {
			a.panel.Invalidate()
		}
		if a.settings != nil {
			a.settings.Invalidate()
		}
		if a.pill != nil {
			a.syncPillSize()
			a.pill.Invalidate()
		}
		a.updateTray()
	})
}

func (a *application) updateTray() {
	if a.tray == nil {
		return
	}
	st := a.store.ViewState()
	zh := st.Prefs.IsChinese()
	now := time.Now()

	pct := 0
	hasUsage := false
	var tipParts []string
	tipParts = append(tipParts, "Claude 用量")
	if !zh {
		tipParts[0] = "Claude Usage"
	}

	if st.OfficialUsage != nil {
		eff := st.OfficialUsage.EffectiveAt(now)
		if eff.FiveHour != nil {
			pct = eff.FiveHour.Percent()
			hasUsage = true
			if zh {
				tipParts = append(tipParts, fmt.Sprintf("5小时 %d%%", pct))
			} else {
				tipParts = append(tipParts, fmt.Sprintf("5h %d%%", pct))
			}
		}
		if eff.SevenDay != nil {
			if zh {
				tipParts = append(tipParts, fmt.Sprintf("本周 %d%%", eff.SevenDay.Percent()))
			} else {
				tipParts = append(tipParts, fmt.Sprintf("Week %d%%", eff.SevenDay.Percent()))
			}
		}
	}
	if st.Snapshot != nil && st.Snapshot.Today() != nil && st.Snapshot.Today().Cost > 0 {
		m := core.NewMoneyFormat(st.Prefs.Currency)
		if zh {
			tipParts = append(tipParts, "今日 "+m.Whole(st.Snapshot.Today().Cost))
		} else {
			tipParts = append(tipParts, "Today "+m.Whole(st.Snapshot.Today().Cost))
		}
	}

	iconBytes := platform.RenderTrayIcon(st.Prefs.IconStyle, pct, hasUsage, st.Shield, a.tray.Dark())
	a.tray.SetIcon(iconBytes, false)
	a.tray.SetToolTip(strings.Join(tipParts, " · "))
}

func (a *application) togglePanel() {
	if a.panel != nil {
		a.panel.Close()
		return
	}
	a.openPanel()
}

func (a *application) openPanel() {
	if a.panel != nil {
		a.panel.Show()
		a.panel.Focus()
		return
	}
	a.store.OnPanelOpened()
	a.panel = mygo.NewFlyout(mygo.FlyoutOptions{
		Tray:         a.tray,
		Placement:    mygo.PlacementBottom,
		Gap:          6,
		Width:        int(appui.PopoverWidth),
		Height:       620,
		FitContent:   true,
		MinWidth:     int(appui.PopoverWidth),
		MaxWidth:     int(appui.PopoverWidth),
		MinHeight:    180,
		MaxHeight:    0, // Let FitContent grow up to the display's work area (matching macOS visibleFrame)
		Focusable:    true,
		Shadow:       true,
		Popover:      true,
		Vibrancy:     mygo.VibrancyPopover,
		CornerRadius: int(appui.PopoverRadius),
		Content:      ui.View(a.popoverModel.View),
	})
	a.panel.OnClosed(func() {
		a.panel = nil
		a.popoverModel.ResetHover()
	})
}

func (a *application) openSettings() {
	if a.panel != nil {
		a.panel.Close()
	}
	if a.settings != nil {
		a.settings.Show()
		a.settings.Focus()
		return
	}
	st := a.store.ViewState()
	title := "设置 — Claude 用量"
	if !st.Prefs.IsChinese() {
		title = "Settings — Claude Usage"
	}
	a.settings = mygo.NewWindow(mygo.WindowOptions{
		Title:     title,
		Width:     540,
		Height:    620,
		MinWidth:  480,
		MinHeight: 500,
		Content:   ui.View(a.settingsModel.View),
	})
	a.settings.Center()
	a.settings.OnClosed(func() {
		a.settings = nil
	})
}

func (a *application) defaultPillPosition(width int) (int, int) {
	disp := mygo.Screen.PrimaryDisplay()
	wa := disp.WorkArea
	if wa.Width > 0 && wa.Height > 0 {
		return wa.X + wa.Width - width - 16, wa.Y + wa.Height - appui.PillHeight - 14
	}
	return 100, 100
}

func isPillPositionVisible(x, y, w, h int) bool {
	displays := mygo.Screen.Displays()
	if len(displays) == 0 {
		displays = []mygo.Display{mygo.Screen.PrimaryDisplay()}
	}
	for _, d := range displays {
		wa := d.WorkArea
		if wa.Width <= 0 || wa.Height <= 0 {
			continue
		}
		ovW := min(x+w, wa.X+wa.Width) - max(x, wa.X)
		ovH := min(y+h, wa.Y+wa.Height) - max(y, wa.Y)
		if ovW >= 32 && ovH >= 20 {
			return true
		}
	}
	return false
}

func (a *application) scheduleSavePillPos() {
	if !a.pillReady || a.pill == nil {
		return
	}
	if a.pillSaveTimer != nil {
		a.pillSaveTimer.Stop()
	}
	a.pillSaveTimer = time.AfterFunc(250*time.Millisecond, func() {
		mygo.RunOnMain(func() {
			if a.pill == nil || !a.pillReady {
				return
			}
			x, y := a.pill.Position()
			if !isPillPositionVisible(x, y, max(a.pillWidth, 120), appui.PillHeight) {
				return
			}
			px, py := x, y
			a.store.UpdatePrefs(func(p *core.Preferences) {
				p.PillPosX = &px
				p.PillPosY = &py
			})
		})
	})
}

func (a *application) setPillAlwaysOnTop(on bool) {
	if a.pill != nil {
		a.pill.SetAlwaysOnTop(on)
	}
}

func (a *application) startPillDrag(dx, dy int) {
	w := a.pill
	if w == nil {
		return
	}
	// Run on the next turn of the Windows main message loop (after View finishes)
	// so ReleaseCapture + modal WM_NCLBUTTONDOWN(HTCAPTION) never re-enter View().
	go func() {
		mygo.RunOnMain(func() {
			if a.pill != w || !platform.IsPrimaryMouseDown() {
				return
			}
			if dx != 0 || dy != 0 {
				wx, wy := w.Position()
				w.SetPosition(wx+dx, wy+dy)
			}
			platform.StartWindowDrag(w.NativeHandle())
			a.scheduleSavePillPos()
		})
	}()
}

func (a *application) resetPillPosition() {
	if a.pill == nil {
		return
	}
	if a.pillSaveTimer != nil {
		a.pillSaveTimer.Stop()
		a.pillSaveTimer = nil
	}
	w := a.pillWidth
	if w <= 0 {
		w = appui.PillTargetWidth(a.store.ViewState())
	}
	x, y := a.defaultPillPosition(w)
	a.pillReady = false
	a.pill.SetPosition(x, y)
	a.pillReady = true
}

func (a *application) syncPillSize() {
	if a.pill == nil {
		return
	}
	targetW := appui.PillTargetWidth(a.store.ViewState())
	if targetW == a.pillWidth {
		return
	}
	a.pillWidth = targetW
	a.pill.SetSize(targetW, appui.PillHeight)
}

func (a *application) setFloatingPill(show bool) {
	if !show {
		if a.pillSaveTimer != nil {
			a.pillSaveTimer.Stop()
			a.pillSaveTimer = nil
		}
		a.pillReady = false
		if a.pill != nil {
			a.pill.Close()
			a.pill = nil
		}
		return
	}
	st := a.store.ViewState()
	if a.pill != nil {
		a.pill.SetAlwaysOnTop(st.Prefs.PillAlwaysOnTop)
		a.syncPillSize()
		a.pill.Show()
		return
	}
	pillW := appui.PillTargetWidth(st)
	a.pillWidth = pillW
	a.pillReady = false
	w := mygo.NewWindow(mygo.WindowOptions{
		Title:             "Claude Usage Pill",
		Width:             pillW,
		Height:            appui.PillHeight,
		Frameless:         true,
		Transparent:       true,
		AlwaysOnTop:       st.Prefs.PillAlwaysOnTop,
		SkipTaskbar:       true,
		DisableResize:     true,
		DisableMinimize:   true,
		DisableMaximize:   true,
		DisableFullScreen: true,
		Content:           ui.View(a.pillModel.View),
	})
	x, y := a.defaultPillPosition(pillW)
	if st.Prefs.PillPosX != nil && st.Prefs.PillPosY != nil &&
		isPillPositionVisible(*st.Prefs.PillPosX, *st.Prefs.PillPosY, pillW, appui.PillHeight) {
		x, y = *st.Prefs.PillPosX, *st.Prefs.PillPosY
	}
	w.SetPosition(x, y)
	w.OnMove(func() {
		a.scheduleSavePillPos()
	})
	w.OnClosed(func() {
		if a.pillSaveTimer != nil {
			a.pillSaveTimer.Stop()
			a.pillSaveTimer = nil
		}
		a.pillReady = false
		a.pill = nil
	})
	a.pill = w
	// Allow initial OS placement events to settle before recording user drags.
	time.AfterFunc(300*time.Millisecond, func() {
		mygo.RunOnMain(func() {
			if a.pill == w {
				a.pillReady = true
			}
		})
	})
}

func (a *application) chooseClaudeDir() {
	go func() {
		cur := a.store.ViewState().ClaudeConfigDir
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Title:           "选择 .claude 目录",
			DefaultPath:     cur,
			Directory:       true,
			ShowHiddenFiles: true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		a.store.UpdatePrefs(func(p *core.Preferences) {
			p.ClaudeConfigDir = paths[0]
		})
	}()
}

func (a *application) chooseDataDir() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Title:           "选择 Claude Code 会话日志目录",
			Directory:       true,
			ShowHiddenFiles: true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		a.store.UpdatePrefs(func(p *core.Preferences) {
			p.CustomDataDir = paths[0]
		})
	}()
}

func (a *application) ready() {
	a.store = service.NewStore(a.invalidateAll)

	a.popoverModel = appui.NewPopoverModel(a.store, appui.PopoverActions{
		OpenSettings: a.openSettings,
		Quit:         mygo.App.Quit,
		ChooseDir:    a.chooseDataDir,
		TogglePill: func() {
			st := a.store.ViewState()
			next := !st.Prefs.ShowFloatingPill
			a.store.UpdatePrefs(func(p *core.Preferences) { p.ShowFloatingPill = next })
			a.setFloatingPill(next)
		},
	})
	a.settingsModel = appui.NewSettingsModel(a.store, appui.SettingsActions{
		ChooseClaudeDir:    a.chooseClaudeDir,
		ChooseDir:          a.chooseDataDir,
		ToggleFloatingPill: a.setFloatingPill,
		SetPillAlwaysOnTop: a.setPillAlwaysOnTop,
		SyncPillSize:       a.syncPillSize,
		SyncTray:           a.updateTray,
		GetOpenAtLogin:     mygo.App.OpenAtLogin,
		SetOpenAtLogin:     mygo.App.SetOpenAtLogin,
	})
	a.pillModel = appui.NewFloatingPillModel(a.store, appui.PillActions{
		OnOpenPanel:    a.togglePanel,
		SetAlwaysOnTop: a.setPillAlwaysOnTop,
		ResetPosition:  a.resetPillPosition,
		HidePill:       func() { a.setFloatingPill(false) },
		StartDrag:      a.startPillDrag,
	})

	var err error
	a.tray, err = mygo.NewTray(mygo.TrayOptions{
		ID:      "ccmonitor-main-tray",
		Icon:    platform.RenderTrayIcon(core.IconStyleClawd, 0, false, platform.ShieldNone, true),
		ToolTip: "Claude 用量",
	})
	if err != nil {
		log.Fatal("tray init failed: ", err)
	}

	a.tray.OnAppearanceChange(func() { a.updateTray() })
	a.tray.OnClick(a.togglePanel)
	a.tray.OnRightClick(func() {
		if a.panel != nil {
			a.panel.Close()
		}
		st := a.store.ViewState()
		zh := st.Prefs.IsChinese()
		openLbl, refLbl, pillLbl, pillTopLbl, setLbl, quitLbl := "打开面板", "立即刷新", "显示桌面悬浮条", "悬浮条始终置顶", "设置…", "退出"
		if !zh {
			openLbl, refLbl, pillLbl, pillTopLbl, setLbl, quitLbl = "Open Panel", "Refresh Now", "Show Floating Bar", "Keep Floating Bar on Top", "Settings…", "Quit"
		}
		items := []*mygo.MenuItem{
			{Label: openLbl, Click: func(*mygo.MenuItem, *mygo.Window) { a.openPanel() }},
			{Label: refLbl, Click: func(*mygo.MenuItem, *mygo.Window) { a.store.RefreshNow() }},
			mygo.Separator(),
			{
				Label:   pillLbl,
				Type:    mygo.MenuItemCheckbox,
				Checked: st.Prefs.ShowFloatingPill,
				Click: func(item *mygo.MenuItem, _ *mygo.Window) {
					a.store.UpdatePrefs(func(p *core.Preferences) { p.ShowFloatingPill = item.Checked })
					a.setFloatingPill(item.Checked)
				},
			},
		}
		if st.Prefs.ShowFloatingPill {
			items = append(items, &mygo.MenuItem{
				Label:   pillTopLbl,
				Type:    mygo.MenuItemCheckbox,
				Checked: st.Prefs.PillAlwaysOnTop,
				Click: func(item *mygo.MenuItem, _ *mygo.Window) {
					a.store.UpdatePrefs(func(p *core.Preferences) { p.PillAlwaysOnTop = item.Checked })
					a.setPillAlwaysOnTop(item.Checked)
				},
			})
		}
		items = append(items,
			&mygo.MenuItem{Label: setLbl, Click: func(*mygo.MenuItem, *mygo.Window) { a.openSettings() }},
			mygo.Separator(),
			&mygo.MenuItem{Label: quitLbl, Role: mygo.RoleQuit},
		)
		menu := mygo.NewMenu(items)
		a.tray.SetMenu(menu)
		defer a.tray.SetMenu(nil)
		a.tray.PopUpMenu()
	})

	if a.demoMode {
		a.store.SetDemoState()
	} else {
		a.store.Start()
	}
	a.updateTray()

	if a.store.ViewState().Prefs.ShowFloatingPill {
		a.setFloatingPill(true)
	}

	// Open panel on first launch (unless started silently at Windows login)
	if a.openOnStart || !mygo.App.WasOpenedAtLogin() {
		a.openPanel()
	}

	mygo.App.OnSecondInstance(func(_ []string, _ string) {
		a.openPanel()
	})

	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		a.quitting = true
	})
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		if a.store != nil {
			a.store.Stop()
		}
		if a.tray != nil {
			a.tray.Destroy()
		}
	})
}

func main() {
	mygo.App.SetName("Claude Usage Monitor")
	if !mygo.App.RequestSingleInstanceLock() {
		os.Exit(0)
	}
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	// Keep running in the system tray when flyout/settings windows close.
	mygo.App.OnWindowAllClosed(func() {})

	app := &application{
		openOnStart: slices.Contains(os.Args[1:], "--open") || slices.Contains(os.Args[1:], "--demo"),
		demoMode:    slices.Contains(os.Args[1:], "--demo"),
	}
	mygo.App.WhenReady(app.ready)

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
