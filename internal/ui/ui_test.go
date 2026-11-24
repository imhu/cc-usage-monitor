package appui

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"ccmonitor/internal/core"
	"ccmonitor/internal/service"
)

func TestPopoverAndSettingsRender(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	store := service.NewStore(nil)
	store.SetDemoState()

	pop := NewPopoverModel(store, PopoverActions{})
	tt := ui.NewTesterWith(pop.View, 356, 720, ui.TesterOptions{Dark: true})

	for _, want := range []string{"Claude 用量", "美国", "美区", "用量限额", "5 小时", "8%", "10%", "输出速度", "71", "本期消耗", "$308.82", "缓存命中率", "99.4%", "Opus 5.5", "设置…", "立即刷新", "退出"} {
		if !tt.HasText(want) {
			t.Errorf("popover missing expected text %q; got texts: %q", want, tt.Texts())
		}
	}

	// Render PNG snapshot if CCMONITOR_SNAPSHOT_DIR is set (for visual inspection!)
	snapDir := os.Getenv("CCMONITOR_SNAPSHOT_DIR")
	saveSnap := func(name string, tester *ui.Tester) {
		if snapDir == "" {
			return
		}
		_ = os.MkdirAll(snapDir, 0o755)
		if img := tester.Image(); img != nil {
			f, err := os.Create(filepath.Join(snapDir, name))
			if err == nil {
				_ = png.Encode(f, img)
				f.Close()
			}
		}
	}
	saveSnap("popover.png", tt)

	// Test Settings tabs
	set := NewSettingsModel(store, SettingsActions{})
	st := ui.NewTesterWith(set.View, 540, 620, ui.TesterOptions{Dark: true})
	if !st.HasText("Claude 官方用量") {
		t.Errorf("settings Usage tab missing expected text; got: %q", st.Texts())
	}
	saveSnap("settings-usage.png", st)

	if err := st.Click("菜单栏"); err != nil {
		t.Fatalf("click Menubar tab failed: %v", err)
	}
	if !st.HasText("显示数值") || !st.HasText("悬浮条始终置顶") {
		t.Errorf("settings Menubar tab missing expected text; got: %q", st.Texts())
	}
	saveSnap("settings-menubar.png", st)

	if err := st.Click("面板"); err != nil {
		t.Fatalf("click Panel tab failed: %v", err)
	}
	if !st.HasText("用量限额") {
		t.Errorf("settings Panel tab missing expected text; got: %q", st.Texts())
	}
	saveSnap("settings-panel.png", st)

	if err := st.Click("数据"); err != nil {
		t.Fatalf("click Data tab failed: %v", err)
	}
	if !st.HasText("自定义 .claude 目录位置") || !st.HasText("Antigravity 数据源") {
		t.Errorf("settings Data tab missing expected directory settings; got: %q", st.Texts())
	}
	saveSnap("settings-data.png", st)

	// Test Floating Pill
	pill := NewFloatingPillModel(store, PillActions{})
	pw := PillTargetWidth(store.ViewState())
	pt := ui.NewTesterWith(pill.View, pw, PillHeight, ui.TesterOptions{Dark: true})
	if !pt.HasText("8%") || !pt.HasText("10%") {
		t.Errorf("floating pill missing expected metrics; got: %q", pt.Texts())
	}
	saveSnap("pill.png", pt)

	// Test Detailed Speed mode render (scatter chart + centered axis)
	store.UpdatePrefs(func(p *core.Preferences) { p.SpeedDisplay = core.DisplayDetailed })
	store.SetDemoState()
	dt := ui.NewTesterWith(pop.View, 356, 900, ui.TesterOptions{Dark: true})
	for _, want := range []string{"60 分钟前", "30 分钟前", "现在", "1 小时", "设置…", "立即刷新", "退出"} {
		if !dt.HasText(want) {
			t.Errorf("detailed speed popover missing %q; got: %q", want, dt.Texts())
		}
	}

	// Even when constrained to a short viewport (e.g. 600px on small/scaled screens),
	// the body must scroll while keeping header and footer pinned inside the viewport.
	short := ui.NewTesterWith(pop.View, 356, 600, ui.TesterOptions{Dark: true})
	for _, footerBtn := range []string{"设置…", "立即刷新", "退出"} {
		r, ok := short.Find(footerBtn)
		if !ok {
			t.Fatalf("short viewport missing footer button %q", footerBtn)
		}
		if r.Y+r.H > 600 {
			t.Fatalf("footer button %q clipped outside 600px viewport: %+v", footerBtn, r)
		}
	}
}

func TestSpeedHelpersMatchOriginal(t *testing.T) {
	// speedReplyDetail divides by displayed 1-decimal seconds so mental math matches.
	// 100 tokens over 0.96s displays "1.0 秒" -> 100 / 1.0 = 100 tok/s (not 100 / 0.96 = 104).
	end := float64(time.Date(2026, 11, 23, 12, 0, 10, 0, time.Local).Unix())
	got := speedReplyDetail(core.TimedRequest{Start: end - 0.96, End: end, Output: 100}, true)
	if !strings.Contains(got, "100 tokens ÷ 1.0 秒 = 100 tok/s") {
		t.Fatalf("unexpected speedReplyDetail: %q", got)
	}
	// Duration rounding to 0.0s must display "–" instead of Infinity/NaN.
	zero := speedReplyDetail(core.TimedRequest{Start: end - 0.02, End: end, Output: 50}, false)
	if !strings.Contains(zero, "50 tokens ÷ 0.0s = – tok/s") {
		t.Fatalf("expected dash rate for 0.0s duration, got: %q", zero)
	}

	// Summary tooltip when untimed vs replied
	if tip := speedSummaryTooltip(nil, nil, nil, false, true, true); !strings.Contains(tip, "2.1.304") {
		t.Fatalf("expected 2.1.304 untimed tooltip, got: %q", tip)
	}
	if tip := speedSummaryTooltip(nil, nil, nil, false, false, true); tip != "" {
		t.Fatalf("expected empty tooltip when no replies in last hour, got: %q", tip)
	}
}

func TestFloatingPillClickVsDrag(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	store := service.NewStore(nil)
	store.SetDemoState()

	opened := 0
	dragCalls := 0
	dragDX, dragDY := 0, 0
	pill := NewFloatingPillModel(store, PillActions{
		OnOpenPanel: func() { opened++ },
		StartDrag: func(dx, dy int) {
			dragCalls++
			dragDX, dragDY = dx, dy
		},
	})
	pw := PillTargetWidth(store.ViewState())
	pt := ui.NewTesterWith(pill.View, pw, PillHeight, ui.TesterOptions{Dark: true})

	// 1. Single-click anywhere on the pill opens the main panel.
	pt.ClickAt(float32(pw/2), float32(PillHeight/2))
	if opened != 1 || dragCalls != 0 {
		t.Fatalf("expected single-click to open panel once without drag, got opened=%d dragCalls=%d", opened, dragCalls)
	}

	// 2. Press + move >= 4px hands off to native StartDrag and must NOT open the panel on release.
	pt.Press(float32(pw/2), float32(PillHeight/2))
	pt.Move(float32(pw/2)+10, float32(PillHeight/2)+5)
	pt.Release(float32(pw/2)+10, float32(PillHeight/2)+5)
	if dragCalls != 1 || dragDX != 10 || dragDY != 5 {
		t.Fatalf("expected single StartDrag(10, 5), got calls=%d (%d, %d)", dragCalls, dragDX, dragDY)
	}
	if opened != 1 {
		t.Fatalf("expected drag release not to open panel, got opened=%d", opened)
	}

	// 3. Subsequent click still opens the panel cleanly.
	pt.ClickAt(float32(pw/2), float32(PillHeight/2))
	if opened != 2 {
		t.Fatalf("expected click after drag to open panel, got opened=%d", opened)
	}
}

func TestAntigravityPopoverSwitchAndRender(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	store := service.NewStore(nil)
	store.SetDemoState()

	changedCount := 0
	pop := NewPopoverModel(store, PopoverActions{
		OnProviderChanged: func() { changedCount++ },
	})
	tt := ui.NewTesterWith(pop.View, 356, 760, ui.TesterOptions{Dark: true})

	// Starts on Claude view
	if !tt.HasText("Claude 用量") {
		t.Fatalf("expected initial Claude view, got: %q", tt.Texts())
	}

	snapDir := os.Getenv("CCMONITOR_SNAPSHOT_DIR")
	saveSnap := func(name string, tester *ui.Tester) {
		if snapDir == "" {
			return
		}
		_ = os.MkdirAll(snapDir, 0o755)
		if img := tester.Image(); img != nil {
			if f, err := os.Create(filepath.Join(snapDir, name)); err == nil {
				_ = png.Encode(f, img)
				f.Close()
			}
		}
	}

	// Open header dropdown menu, verify Antigravity item with live 5h%, then close by clicking anchor again
	if err := tt.Click("Claude 用量"); err != nil {
		t.Fatalf("click Claude header trigger failed: %v", err)
	}
	if !tt.HasText("Antigravity") || !tt.HasText("42%") {
		t.Fatalf("expected dropdown menu with Antigravity (42%%), got: %q", tt.Texts())
	}
	saveSnap("popover-dropdown.png", tt)

	if err := tt.Click("Claude 用量"); err != nil {
		t.Fatalf("re-click Claude header trigger failed: %v", err)
	}
	if tt.HasText("Antigravity") {
		t.Fatalf("expected dropdown menu to close on second trigger click, still got: %q", tt.Texts())
	}

	// Open dropdown and click Antigravity to switch view
	if err := tt.Click("Claude 用量"); err != nil {
		t.Fatalf("click Claude header trigger failed: %v", err)
	}
	if err := tt.Click("Antigravity"); err != nil {
		t.Fatalf("click Antigravity dropdown item failed: %v", err)
	}
	if changedCount != 1 {
		t.Fatalf("expected OnProviderChanged=1, got %d", changedCount)
	}
	for _, want := range []string{
		"Antigravity 用量", "ULTRA", "活跃进程", "用量限额",
		"Gemini 5小时", "42%", "Gemini 每周", "31%",
		"输出速度", "~68", "本期消耗", "$186.20", "83.4%",
		"Gemini 4 Neon", "Claude Sonnet 4.5", "设置…", "立即刷新", "退出",
	} {
		if !tt.HasText(want) {
			t.Errorf("antigravity popover missing expected text %q; got texts: %q", want, tt.Texts())
		}
	}

	saveSnap("popover-ag.png", tt)

	// Antigravity Floating Pill renders cleanly
	pill := NewFloatingPillModel(store, PillActions{})
	pw := PillTargetWidth(store.ViewState())
	pt := ui.NewTesterWith(pill.View, pw, PillHeight, ui.TesterOptions{Dark: true})
	if !pt.HasText("42%") {
		t.Errorf("antigravity floating pill missing 42%%; got: %q", pt.Texts())
	}
	saveSnap("pill-ag.png", pt)

	// Short viewport (600px) keeps Antigravity footer visible inside bounds
	short := ui.NewTesterWith(pop.View, 356, 600, ui.TesterOptions{Dark: true})
	for _, footerBtn := range []string{"设置…", "立即刷新", "退出"} {
		r, ok := short.Find(footerBtn)
		if !ok {
			t.Fatalf("short AG viewport missing footer button %q", footerBtn)
		}
		if r.Y+r.H > 600 {
			t.Fatalf("footer button %q clipped outside 600px viewport: %+v", footerBtn, r)
		}
	}

	// Switch back to Claude cleanly via header dropdown
	if err := tt.Click("Antigravity 用量"); err != nil {
		t.Fatalf("click Antigravity header trigger failed: %v", err)
	}
	if err := tt.Click("Claude"); err != nil {
		t.Fatalf("click Claude dropdown item failed: %v", err)
	}
	if !tt.HasText("Claude 用量") || changedCount != 2 {
		t.Fatalf("expected switch back to Claude view, changedCount=%d", changedCount)
	}

	// Test Settings Menubar Antigravity preview tab
	set := NewSettingsModel(store, SettingsActions{})
	st := ui.NewTesterWith(set.View, 540, 620, ui.TesterOptions{Dark: true})
	if err := st.Click("菜单栏"); err != nil {
		t.Fatalf("click Menubar tab failed: %v", err)
	}
	if err := st.Click("Antigravity"); err != nil {
		t.Fatalf("click Antigravity preview toggle failed: %v", err)
	}
	for _, want := range []string{"Antigravity 显示项", "5H", "WEEK", "CACHE", "TODAY", "CTX"} {
		if !st.HasText(want) {
			t.Errorf("settings Antigravity menubar tab missing %q; got: %q", want, st.Texts())
		}
	}
	saveSnap("settings-menubar-ag.png", st)
}
