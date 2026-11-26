package appui

import (
	"image/png"
	"math"
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
		"Antigravity 用量", "Ultra", "美国", "美区", core.MaskIP("162.141.213.48"), "活跃进程", "用量限额",
		"Gemini", "5 小时", "42%", "本周", "31%", "第三方模型", "本周期未使用", "0%",
		"输出速度", "~68", "本期消耗", "$186.20", "83.4%",
		"Gemini 4 Neon", "Claude Sonnet 4.5", "设置…", "立即刷新", "退出",
	} {
		if !tt.HasText(want) {
			t.Errorf("antigravity popover missing expected text %q; got texts: %q", want, tt.Texts())
		}
	}

	saveSnap("popover-ag.png", tt)

	// Unused Third-Party group expands on click and collapses back on "收起"
	if err := tt.Click("第三方模型"); err != nil {
		t.Fatalf("click unused Third-Party group failed: %v", err)
	}
	if !tt.HasText("收起") {
		t.Fatalf("expected expanded Third-Party group with 收起, got: %q", tt.Texts())
	}
	if err := tt.Click("收起"); err != nil {
		t.Fatalf("click collapse 收起 failed: %v", err)
	}
	if !tt.HasText("本周期未使用") {
		t.Fatalf("expected collapsed Third-Party group with 本周期未使用, got: %q", tt.Texts())
	}

	// Antigravity Floating Pill auto-sizes tightly in single-, 4-, and 5-column modes
	reportedW := 0
	pill := NewFloatingPillModel(store, PillActions{
		OnContentWidth: func(w int) { reportedW = w },
	})
	pw := PillTargetWidth(store.ViewState())
	pt := ui.NewTesterWith(pill.View, pw, PillHeight, ui.TesterOptions{Dark: true})
	if !pt.HasText("42%") {
		t.Errorf("antigravity floating pill missing 42%%; got: %q", pt.Texts())
	}

	store.UpdatePrefs(func(p *core.Preferences) {
		p.AGPillShowFiveHour = true
		p.AGPillShowWeek = true
		p.AGPillShowCache = true
		p.AGPillShowTodayCost = true
		p.AGPillShowContext = false
	})
	pw4 := PillTargetWidth(store.ViewState())
	if pw4 <= pw || pw4 >= 210 {
		t.Fatalf("expected compact 4-column width in (%d, 210), got %d", pw, pw4)
	}

	store.UpdatePrefs(func(p *core.Preferences) {
		p.AGPillShowContext = true
	})
	pw5 := PillTargetWidth(store.ViewState())
	if pw5 <= pw4 {
		t.Fatalf("expected 5-column width (%d) > 4-column width (%d)", pw5, pw4)
	}

	pill.ResetMeasuredWidth()
	ptAll := ui.NewTesterWith(pill.View, pw5, PillHeight, ui.TesterOptions{Dark: true})
	ptAll.Advance(20 * time.Millisecond)
	if reportedW <= 0 || reportedW > 520 {
		t.Fatalf("expected OnContentWidth measured width in (0, 520], got %d", reportedW)
	}
	ptAll.SetSize(reportedW, PillHeight)
	for _, want := range []string{"5小时", "本周", "缓存", "今日", "上下文", "42%"} {
		if !ptAll.HasText(want) {
			t.Errorf("antigravity Chinese multi-column pill missing %q; got: %q", want, ptAll.Texts())
		}
	}
	saveSnap("pill-ag.png", ptAll)

	store.UpdatePrefs(func(p *core.Preferences) { p.Language = core.LangEN })
	ptEn := ui.NewTesterWith(pill.View, PillTargetWidth(store.ViewState()), PillHeight, ui.TesterOptions{Dark: true})
	for _, want := range []string{"5H", "WEEK", "CACHE", "TODAY", "CTX", "42%"} {
		if !ptEn.HasText(want) {
			t.Errorf("antigravity English multi-column pill missing %q; got: %q", want, ptEn.Texts())
		}
	}

	store.UpdatePrefs(func(p *core.Preferences) {
		p.Language = core.LangZH
		p.AGPillShowWeek = false
		p.AGPillShowCache = false
		p.AGPillShowTodayCost = false
		p.AGPillShowContext = false
	})

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

	// Test Settings Menubar Antigravity preview tab + Data All-time card
	set := NewSettingsModel(store, SettingsActions{})
	st := ui.NewTesterWith(set.View, 540, 620, ui.TesterOptions{Dark: true})
	if err := st.Click("菜单栏"); err != nil {
		t.Fatalf("click Menubar tab failed: %v", err)
	}
	if err := st.Click("Antigravity"); err != nil {
		t.Fatalf("click Antigravity preview toggle failed: %v", err)
	}
	for _, want := range []string{"Antigravity 显示项", "5小时", "本周", "缓存", "今日", "上下文"} {
		if !st.HasText(want) {
			t.Errorf("settings Antigravity menubar tab missing %q; got: %q", want, st.Texts())
		}
	}
	saveSnap("settings-menubar-ag.png", st)

	if err := st.Click("数据"); err != nil {
		t.Fatalf("click Data tab failed: %v", err)
	}
	for _, want := range []string{"累计 API 等效用量", "$1,284.60"} {
		if !st.HasText(want) {
			t.Errorf("settings Data tab missing all-time usage text %q; got: %q", want, st.Texts())
		}
	}
	stTall := ui.NewTesterWith(set.View, 540, 1060, ui.TesterOptions{Dark: true})
	saveSnap("settings-data-ag.png", stTall)
}

func TestSpendMathRoundingTooltipsAndLiveSettings(t *testing.T) {
	now := time.Date(2026, 11, 24, 14, 0, 0, 0, time.Local)
	wStart := now.Add(-2 * 24 * time.Hour)
	wEnd := wStart.Add(core.WeekWindow)
	restartedAt := now.Add(-12 * time.Hour)

	// Displayed projected estimate is $100 (from $100.40 fullWeek), spent is $30.25 ->
	// exactRemaining must be $69.75 so 30.25 + 69.75 = 100.00!
	snap := &core.UsageSnapshot{
		WeeklyAllowance: &core.WeeklyAllowance{
			Start:       wStart,
			End:         wEnd,
			Utilization: 50,
			SyncedCost:  30.20, // Full = $60.40
			Spent:       10.25,
			RestartedAt: &restartedAt,
			Forgiven:    40.00, // FullWeek = $100.40 -> Whole displays "$100"
		},
	}
	period := &core.PeriodUsage{
		Start:    wStart,
		End:      wEnd,
		Cost:     30.25,
		DayIndex: 3,
		DayCount: 7,
	}

	sm := spendMath{snap: snap, money: core.NewMoneyFormat(core.CurrencyUSD), now: now, zh: true}
	if got := sm.projectedText(period); got != "$100" {
		t.Fatalf("expected projected $100, got %q", got)
	}
	exact, ok := sm.exactRemaining(period)
	if !ok || exact != "$69.75" {
		t.Fatalf("expected exactRemaining $69.75 matching displayed $100 - $30.25, got %q (ok=%v)", exact, ok)
	}
	tip := sm.totalTooltip(period, core.SpanWeek, false)
	if !strings.Contains(tip, "重置前已花的 $40.00") || !strings.Contains(tip, "$60.00") {
		t.Fatalf("expected midweek reset split ($40.00 + $60.00 = $100) in totalTooltip, got: %q", tip)
	}
	paceTip := sm.paceTooltip(period, core.SpanWeek)
	if !strings.Contains(paceTip, "中途重置了本周额度") || !strings.Contains(paceTip, "$69.75") {
		t.Fatalf("expected midweek reset basis & exact remaining in paceTooltip, got: %q", paceTip)
	}

	// runsOutNote formatting
	if got := runsOutNote(now.Add(2*time.Hour), now, true); !strings.Contains(got, "16:00 用完") {
		t.Fatalf("expected same-day HH:mm runsOutNote, got %q", got)
	}
	if got := runsOutNote(now.Add(24*time.Hour), now, true); got != "明天用完" {
		t.Fatalf("expected tomorrow runsOutNote, got %q", got)
	}
	if got := runsOutNote(now.Add(-time.Minute), now, true); got != "额度已用完" {
		t.Fatalf("expected past runsOutNote, got %q", got)
	}

	// 429 status tooltip + Claude Code source
	st429 := service.ViewState{
		OfficialState:       service.OfficialFailed,
		IsRateLimited:       true,
		RetryWait:           4 * time.Minute,
		UsageFromClaudeCode: true,
		OfficialUsage: &core.OfficialUsage{
			FetchedAt: now.Add(-5 * time.Minute),
		},
	}
	statusTip := limitsStatusTooltip(st429, limitsStaleReason(st429, true), true)
	if !strings.Contains(statusTip, "Claude Code 查到的") || !strings.Contains(statusTip, "共用") {
		t.Fatalf("expected Claude Code source & 429 shared-allowance note, got: %q", statusTip)
	}

	// Settings > Menubar shows live "–" for speed when no 5m replies (never static "71")
	t.Setenv("LOCALAPPDATA", t.TempDir())
	emptyStore := service.NewStore(nil)
	set := NewSettingsModel(emptyStore, SettingsActions{})
	set.ActiveTab = TabMenuBar
	stUI := ui.NewTesterWith(set.View, 540, 620, ui.TesterOptions{Dark: true})
	if stUI.HasText("71") {
		t.Fatalf("settings Menubar tab must not show hardcoded 71 speed when no replies exist; got: %q", stUI.Texts())
	}
	if !stUI.HasText("–") {
		t.Fatalf("settings Menubar tab should show live dash for speed with no 5m replies; got: %q", stUI.Texts())
	}
}

func TestAntigravityDailyViewOtherModelsAndSlidingTabs(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	store := service.NewStore(nil)
	store.SetDemoState()

	pop := NewPopoverModel(store, PopoverActions{})
	tt := ui.NewTesterWith(pop.View, 356, 820, ui.TesterOptions{Dark: true})

	// Test sliding spend tabs on Claude panel
	if err := tt.Click("每日"); err != nil {
		t.Fatalf("click Claude 每日 tab failed: %v", err)
	}
	tt.Advance(200 * time.Millisecond)
	if !tt.HasText("当天") {
		t.Fatalf("expected Claude daily view to show 当天, got: %q", tt.Texts())
	}
	if err := tt.Click("本周期"); err != nil {
		t.Fatalf("click Claude 本周期 tab failed: %v", err)
	}
	tt.Advance(200 * time.Millisecond)

	// Switch to Antigravity and test 每日 view (no duplicate 省 under big amount)
	if err := tt.Click("Claude 用量"); err != nil {
		t.Fatalf("open provider dropdown failed: %v", err)
	}
	if err := tt.Click("Antigravity"); err != nil {
		t.Fatalf("switch to Antigravity failed: %v", err)
	}
	if err := tt.Click("每日"); err != nil {
		t.Fatalf("click Antigravity 每日 tab failed: %v", err)
	}
	tt.Advance(200 * time.Millisecond)
	if !tt.HasText("当天") {
		t.Fatalf("expected Antigravity daily view to show 当天, got: %q", tt.Texts())
	}
	savedCount := 0
	for _, txt := range tt.Texts() {
		if strings.HasPrefix(txt, "省 ") {
			savedCount++
		}
	}
	if savedCount != 1 {
		t.Fatalf("expected 省 $xxx once (in cache hit row only), got %d occurrences in: %q", savedCount, tt.Texts())
	}

	// Verify >3 models renders top 2 + 其他
	c1, c2, c3, c4 := 12.50, 8.20, 3.10, 1.20
	fourModels := []core.AGModelUsageSummary{
		{Model: "claude-opus-4-6-thinking", DisplayName: "Opus 4.6", OutputTokens: 1000, EstimatedCostUSD: &c1},
		{Model: "gemini-4-neon-xhigh", DisplayName: "Gemini 4 Neon", OutputTokens: 2000, EstimatedCostUSD: &c2},
		{Model: "gemini-2.5-flash", DisplayName: "Gemini 2.5 Flash", OutputTokens: 3000, EstimatedCostUSD: &c3},
		{Model: "gpt-oss-120b", DisplayName: "GPT OSS 120B", OutputTokens: 4000, EstimatedCostUSD: &c4},
	}
	mt := ui.NewTesterWith(func(c *ui.Context) {
		ui.Column(c).FillWidth().Padding(8).Children(func() {
			pop.renderAGModelList(c, fourModels, 25.00, core.NewMoneyFormat(core.CurrencyUSD), true)
		})
	}, 356, 200, ui.TesterOptions{Dark: true})
	for _, want := range []string{"Opus 4.6", "Gemini 4 Neon", "其他", "$4.30"} {
		if !mt.HasText(want) {
			t.Fatalf("expected %q in merged model list, got: %q", want, mt.Texts())
		}
	}
	if mt.HasText("Gemini 2.5 Flash") || mt.HasText("GPT OSS 120B") {
		t.Fatalf("expected 3rd/4th models merged under 其他, got: %q", mt.Texts())
	}
}

func TestUpdateBadgeAndSettingsGeneralCard(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	store := service.NewStore(nil)
	store.SetDemoState()
	store.SetUpdateReleaseForTest(&core.ReleaseInfo{
		TagName: "v9.9.9",
		Body:    "### Updates\n- Added update card",
		HTMLURL: "https://github.com/imhu/cc-usage-monitor/releases/tag/v9.9.9",
	})

	if !store.ViewState().Update.HasBadge() {
		t.Fatal("expected HasBadge=true when update release is set")
	}

	openUpdateCount := 0
	pop := NewPopoverModel(store, PopoverActions{
		OpenSettingsUpdate: func() { openUpdateCount++ },
	})
	pt := ui.NewTesterWith(pop.View, 356, 760, ui.TesterOptions{Dark: true})
	if err := pt.Click("设置…"); err != nil {
		t.Fatalf("click 设置… failed: %v", err)
	}
	if openUpdateCount != 1 {
		t.Fatalf("expected OpenSettingsUpdate=1 when badge active, got %d", openUpdateCount)
	}

	set := NewSettingsModel(store, SettingsActions{})
	set.OpenUpdateSection()
	st := ui.NewTesterWith(set.View, 540, 760, ui.TesterOptions{Dark: true})
	for _, want := range []string{"软件更新", "v9.9.9", "Added update card", "立即更新并重启", "忽略此版本"} {
		if !st.HasText(want) {
			t.Fatalf("settings General update card missing %q; got: %q", want, st.Texts())
		}
	}

	if err := st.Click("忽略此版本"); err != nil {
		t.Fatalf("click 忽略此版本 failed: %v", err)
	}
	vs := store.ViewState()
	if vs.Prefs.SkippedVersion != "v9.9.9" || vs.Update.HasBadge() {
		t.Fatalf("expected SkippedVersion=v9.9.9 and HasBadge=false, got prefs=%q badge=%v",
			vs.Prefs.SkippedVersion, vs.Update.HasBadge())
	}
}

func TestAntigravityLogoSheenAndPillAnimation(t *testing.T) {
	// 1. Validate agSheenGradient produces sorted stops in [0, 1], <= 7 stops, and zero alpha when intensity == 0.
	for _, center := range []float32{-0.22, 0.0, 0.25, 0.5, 0.75, 1.0, 1.22} {
		g := agSheenGradient(center, 0.28, 1.0)
		if len(g.Stops) < 2 || len(g.Stops) > 7 {
			t.Fatalf("center=%v expected 2..7 stops, got %d", center, len(g.Stops))
		}
		if g.Stops[0].At != 0 || g.Stops[len(g.Stops)-1].At != 1 {
			t.Fatalf("center=%v stops must span [0, 1], got %+v", center, g.Stops)
		}
		for i := 1; i < len(g.Stops); i++ {
			if g.Stops[i].At <= g.Stops[i-1].At {
				t.Fatalf("center=%v stops not strictly increasing: %+v", center, g.Stops)
			}
		}
	}
	zeroG := agSheenGradient(0.5, 0.28, 0)
	for _, s := range zeroG.Stops {
		if s.Color.A != 0 {
			t.Fatalf("expected zero alpha when intensity=0, got %+v", zeroG.Stops)
		}
	}

	// 2. Render Antigravity floating pill across active -> idle transitions and toggle off.
	t.Setenv("LOCALAPPDATA", t.TempDir())
	store := service.NewStore(nil)
	store.SetDemoState()
	store.SetActiveProvider(core.ProviderAntigravity)

	pill := NewFloatingPillModel(store, PillActions{})
	pw := PillTargetWidth(store.ViewState())
	pt := ui.NewTesterWith(pill.View, pw, PillHeight, ui.TesterOptions{Dark: true})
	if !pt.HasText("42%") {
		t.Fatalf("antigravity pill missing 42%%; got %q", pt.Texts())
	}

	store.SetAGExecutionForTest(core.AGExecutionState{
		IsExecuting: true,
		ActiveUntil: time.Now().Add(time.Minute),
	})
	pt.Advance(150 * time.Millisecond)
	if img := pt.Image(); img == nil {
		t.Fatal("expected non-nil rendered pill image while active")
	}

	store.SetAGExecutionForTest(core.AGExecutionState{})
	pt.Advance(350 * time.Millisecond)
	if img := pt.Image(); img == nil {
		t.Fatal("expected non-nil rendered pill image after returning idle")
	}

	// 3. Settings Menubar Antigravity tab renders the toggle and flips AGLogoAnimation.
	set := NewSettingsModel(store, SettingsActions{})
	st := ui.NewTesterWith(set.View, 540, 620, ui.TesterOptions{Dark: true})
	if err := st.Click("菜单栏"); err != nil {
		t.Fatalf("click Menubar tab failed: %v", err)
	}
	if err := st.Click("Antigravity"); err != nil {
		t.Fatalf("click Antigravity preview toggle failed: %v", err)
	}
	if !st.HasText("执行时图标扫光") {
		t.Fatalf("expected 执行时图标扫光 setting row, got: %q", st.Texts())
	}
	if !store.ViewState().Prefs.AGLogoAnimation {
		t.Fatal("expected AGLogoAnimation default true")
	}
	r, ok := st.Find("执行时图标扫光")
	if !ok {
		t.Fatal("expected 执行时图标扫光 bounds")
	}
	st.ClickAt(540-24-22, r.Y+18)
	if store.ViewState().Prefs.AGLogoAnimation {
		t.Fatal("expected AGLogoAnimation toggled to false")
	}
}

func TestGenerateReadmeScreenshots(t *testing.T) {
	outDir := strings.TrimSpace(os.Getenv("CCMONITOR_README_SHOTS"))
	if outDir == "" {
		t.Skip("set CCMONITOR_README_SHOTS to generate README screenshots")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("LOCALAPPDATA", t.TempDir())
	store := service.NewStore(nil)
	store.SetDemoState()

	savePNG := func(name string, tester *ui.Tester) {
		t.Helper()
		img := tester.Image()
		if img == nil {
			t.Fatalf("nil image for %s", name)
		}
		f, err := os.Create(filepath.Join(outDir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}

	const frameW = int(PopoverWidth) + 32

	renderPanelShot := func(filename string, provider core.ProviderKind, lang core.LanguagePref, anchorText, quitText string) {
		t.Helper()
		store.SetActiveProvider(provider)
		store.UpdatePrefs(func(p *core.Preferences) {
			p.Language = lang
		})
		store.SetDemoState()
		pop := NewPopoverModel(store, PopoverActions{})
		framedView := func(c *ui.Context) {
			card := ui.Box(c.Key("readme-card")).
				Width(PopoverWidth).
				FillHeight().
				Radius(PopoverRadius).
				Clip().
				Border(1, ui.RGBA(255, 255, 255, 0.12)).
				Shadow(0, 8, 22, 0, ui.RGBA(0, 0, 0, 0.36))
			card.Children(func() {
				pop.View(c)
			})
			c.Root().Background(ui.Transparent).Padding(12, 16, 22, 16)
		}

		probe := ui.NewTesterWith(framedView, frameW, 880, ui.TesterOptions{Dark: true})
		lastRow, ok := probe.Find(anchorText)
		if !ok {
			t.Fatalf("%s: anchor text %q not found in %q", filename, anchorText, probe.Texts())
		}
		tightH := int(math.Ceil(float64(lastRow.Y+lastRow.H))) + 84

		tt := ui.NewTesterWith(framedView, frameW, tightH, ui.TesterOptions{Dark: true})
		tt.SetScale(2)
		tt.Advance(400 * time.Millisecond)

		qr, ok := tt.Find(quitText)
		if !ok {
			t.Fatalf("%s: quit text %q not found", filename, quitText)
		}
		if qr.Y+qr.H > float32(tightH-22) {
			t.Fatalf("%s: footer %q clipped (bottom=%.1f > limit=%d)", filename, quitText, qr.Y+qr.H, tightH-22)
		}
		savePNG(filename, tt)
	}

	renderPillShot := func(filename string, provider core.ProviderKind, lang core.LanguagePref, activeAG bool) {
		t.Helper()
		store.SetActiveProvider(provider)
		store.UpdatePrefs(func(p *core.Preferences) {
			p.Language = lang
			if provider == core.ProviderAntigravity {
				p.AGPillShowFiveHour = true
				p.AGPillShowWeek = true
				p.AGPillShowCache = true
				p.AGPillShowTodayCost = true
				p.AGPillShowContext = true
			} else {
				p.PillShowFiveHour = true
				p.PillShowWeek = true
				p.PillShowTodayCost = true
			}
		})
		store.SetDemoState()
		if activeAG {
			store.SetAGExecutionForTest(core.AGExecutionState{
				IsExecuting: true,
				ActiveUntil: time.Now().Add(time.Minute),
			})
		} else {
			store.SetAGExecutionForTest(core.AGExecutionState{})
		}

		measuredW := 0
		pill := NewFloatingPillModel(store, PillActions{
			OnContentWidth: func(w int) { measuredW = w },
		})
		initW := PillTargetWidth(store.ViewState())
		pt := ui.NewTesterWith(pill.View, initW, PillHeight, ui.TesterOptions{Dark: true})
		pt.Advance(20 * time.Millisecond)
		if measuredW > 0 {
			pt.SetSize(measuredW, PillHeight)
		}
		pt.SetScale(2)
		if activeAG {
			pt.Advance(600 * time.Millisecond)
		} else {
			pt.Advance(100 * time.Millisecond)
		}
		savePNG(filename, pt)
	}

	renderPanelShot("panel-claude-zh.png", core.ProviderClaude, core.LangZH, "Opus 5.5", "退出")
	renderPanelShot("panel-antigravity-zh.png", core.ProviderAntigravity, core.LangZH, "Claude Sonnet 4.5", "退出")
	renderPillShot("pill-claude-zh.png", core.ProviderClaude, core.LangZH, false)
	renderPillShot("pill-antigravity-zh.png", core.ProviderAntigravity, core.LangZH, true)

	renderPanelShot("panel-claude-en.png", core.ProviderClaude, core.LangEN, "Opus 5.5", "Quit")
	renderPanelShot("panel-antigravity-en.png", core.ProviderAntigravity, core.LangEN, "Claude Sonnet 4.5", "Quit")
	renderPillShot("pill-claude-en.png", core.ProviderClaude, core.LangEN, false)
	renderPillShot("pill-antigravity-en.png", core.ProviderAntigravity, core.LangEN, true)
}
