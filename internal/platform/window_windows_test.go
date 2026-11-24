//go:build windows

package platform

import "testing"

func TestBorderlessPopupStyle(t *testing.T) {
	const (
		wsVisible      = 0x10000000
		wsClipChildren = 0x02000000
	)
	initial := uintptr(wsCaption | wsSysMenu | wsThickFrame | wsMinimizeBox | wsMaximizeBox | wsVisible | wsClipChildren)
	got := borderlessPopupStyle(initial)

	for _, cleared := range []uintptr{wsCaption, wsSysMenu, wsThickFrame, wsMinimizeBox, wsMaximizeBox} {
		if got&cleared != 0 {
			t.Fatalf("expected style bit %#x to be cleared, got %#x", cleared, got)
		}
	}
	if got&uintptr(wsPopup) == 0 {
		t.Fatalf("expected WS_POPUP (%#x) to be set, got %#x", uintptr(wsPopup), got)
	}
	wantPreserved := uintptr(wsVisible | wsClipChildren)
	if got&wantPreserved != wantPreserved {
		t.Fatalf("expected %#x bits to be preserved, got %#x", wantPreserved, got)
	}
}

func TestMakeBorderlessTransparentZeroHWND(t *testing.T) {
	MakeBorderlessTransparent(0)
}
