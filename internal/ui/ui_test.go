package appui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

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
	if !st.HasText("显示数值") {
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
	if !st.HasText("自定义 .claude 目录位置") {
		t.Errorf("settings Data tab missing .claude directory setting; got: %q", st.Texts())
	}
	saveSnap("settings-data.png", st)

	// Test Floating Pill
	pill := NewFloatingPillModel(store, nil)
	pt := ui.NewTesterWith(pill.View, 220, 38, ui.TesterOptions{Dark: true})
	if !pt.HasText("8%") || !pt.HasText("10%") {
		t.Errorf("floating pill missing expected metrics; got: %q", pt.Texts())
	}
	saveSnap("pill.png", pt)
}
