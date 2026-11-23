//go:build windows

package platform

import "golang.org/x/sys/windows"

var (
	modUser32            = windows.NewLazySystemDLL("user32.dll")
	procReleaseCapture   = modUser32.NewProc("ReleaseCapture")
	procSendMessageW     = modUser32.NewProc("SendMessageW")
	procGetAsyncKeyState = modUser32.NewProc("GetAsyncKeyState")
	procGetSystemMetrics = modUser32.NewProc("GetSystemMetrics")
)

const (
	wmNCLButtonDown = 0x00A1
	htCaption       = 2
	vkLButton       = 0x01
	vkRButton       = 0x02
	smSwapButton    = 23
)

// IsPrimaryMouseDown reports whether the logical primary mouse button is currently held down.
func IsPrimaryMouseDown() bool {
	vk := uintptr(vkLButton)
	if r, _, _ := procGetSystemMetrics.Call(smSwapButton); r != 0 {
		vk = vkRButton
	}
	state, _, _ := procGetAsyncKeyState.Call(vk)
	return state&0x8000 != 0
}

// StartWindowDrag hands off an active mouse press on hwnd to the native Windows
// title-bar move loop (ReleaseCapture + WM_NCLBUTTONDOWN / HTCAPTION).
func StartWindowDrag(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	_, _, _ = procReleaseCapture.Call()
	_, _, _ = procSendMessageW.Call(hwnd, wmNCLButtonDown, htCaption, 0)
}
