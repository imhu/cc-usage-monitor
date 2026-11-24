//go:build windows

package platform

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modUser32                     = windows.NewLazySystemDLL("user32.dll")
	modDwmapi                     = windows.NewLazySystemDLL("dwmapi.dll")
	procReleaseCapture            = modUser32.NewProc("ReleaseCapture")
	procSendMessageW              = modUser32.NewProc("SendMessageW")
	procGetAsyncKeyState          = modUser32.NewProc("GetAsyncKeyState")
	procGetSystemMetrics          = modUser32.NewProc("GetSystemMetrics")
	procGetWindowLongPtrW         = modUser32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtrW         = modUser32.NewProc("SetWindowLongPtrW")
	procSetWindowPos              = modUser32.NewProc("SetWindowPos")
	procDwmSetWindowAttribute     = modDwmapi.NewProc("DwmSetWindowAttribute")
	procDwmExtendFrameIntoClient  = modDwmapi.NewProc("DwmExtendFrameIntoClientArea")
)

const (
	wmNCLButtonDown = 0x00A1
	htCaption       = 2
	vkLButton       = 0x01
	vkRButton       = 0x02
	smSwapButton    = 23

	gwlStyle = int32(-16)

	wsPopup       = 0x80000000
	wsCaption     = 0x00C00000
	wsSysMenu     = 0x00080000
	wsThickFrame  = 0x00040000
	wsMinimizeBox = 0x00020000
	wsMaximizeBox = 0x00010000

	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoZOrder     = 0x0004
	swpNoActivate   = 0x0010
	swpFrameChanged = 0x0020

	dwmwaWindowCornerPreference = 33
	dwmwaBorderColor            = 34
	dwmwcpDoNotRound            = int32(1)
	dwmColorNone                = uint32(0xFFFFFFFE)
)

type dwmMargins struct {
	Left   int32
	Right  int32
	Top    int32
	Bottom int32
}

func borderlessPopupStyle(style uintptr) uintptr {
	return (style &^ uintptr(wsCaption|wsSysMenu|wsThickFrame|wsMinimizeBox|wsMaximizeBox)) | uintptr(wsPopup)
}

func getWindowLongStyle(hwnd uintptr) uintptr {
	idx := gwlStyle
	r, _, _ := procGetWindowLongPtrW.Call(hwnd, uintptr(idx))
	return r
}

func setWindowLongStyle(hwnd uintptr, style uintptr) {
	idx := gwlStyle
	_, _, _ = procSetWindowLongPtrW.Call(hwnd, uintptr(idx), style)
}

// MakeBorderlessTransparent switches hwnd to a borderless WS_POPUP window and
// disables Windows 11 DWM rounded corners, frame extension, and borders so
// per-pixel transparent GPU surfaces render without outer corner artifacts.
func MakeBorderlessTransparent(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	if cur := getWindowLongStyle(hwnd); cur != 0 {
		if next := borderlessPopupStyle(cur); next != cur {
			setWindowLongStyle(hwnd, next)
		}
	}
	if procDwmExtendFrameIntoClient.Find() == nil {
		m := dwmMargins{}
		_, _, _ = procDwmExtendFrameIntoClient.Call(hwnd, uintptr(unsafe.Pointer(&m)))
	}
	if procDwmSetWindowAttribute.Find() == nil {
		pref := dwmwcpDoNotRound
		_, _, _ = procDwmSetWindowAttribute.Call(hwnd, dwmwaWindowCornerPreference, uintptr(unsafe.Pointer(&pref)), unsafe.Sizeof(pref))
		borderNone := dwmColorNone
		_, _, _ = procDwmSetWindowAttribute.Call(hwnd, dwmwaBorderColor, uintptr(unsafe.Pointer(&borderNone)), unsafe.Sizeof(borderNone))
	}
	_, _, _ = procSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpFrameChanged)
}

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
