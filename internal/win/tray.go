//go:build windows

package win

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

type notifyIconData struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         windows.GUID
	HBalloonIcon     uintptr
}

const (
	nimAdd        = 0
	nimModify     = 1
	nimDelete     = 2
	nimSetVersion = 4

	nifMessage  = 0x01
	nifIcon     = 0x02
	nifTip      = 0x04
	nifInfo     = 0x10
	nifShowTip  = 0x80
	niifUser    = 0x04
	niifLargeIc = 0x20

	// Callback notifications (NOTIFYICON_VERSION_4)
	NIN_SELECT           = 0x400
	NIN_KEYSELECT        = 0x401
	NIN_BALLOONUSERCLICK = 0x405

	// WM_TRAY is the window message the tray icon sends.
	WM_TRAY = WM_APP + 1
)

type Tray struct {
	hwnd  uintptr
	icon  uintptr
	tip   string
	added bool
}

func NewTray(hwnd uintptr, dpi int) *Tray {
	return &Tray{hwnd: hwnd, icon: LoadAppIcon(SmallIconSize(dpi))}
}

func (t *Tray) data() notifyIconData {
	var d notifyIconData
	d.CbSize = uint32(unsafe.Sizeof(d))
	d.HWnd = t.hwnd
	d.UID = 1
	return d
}

func copyStr(dst []uint16, s string) {
	u, _ := windows.UTF16FromString(s)
	if len(u) > len(dst) {
		u = u[:len(dst)]
		u[len(u)-1] = 0
	}
	copy(dst, u)
}

// Add (re-)adds the icon; call again after Explorer restarts (TaskbarCreated).
func (t *Tray) Add(tip string) {
	t.tip = tip
	d := t.data()
	d.UFlags = nifMessage | nifIcon | nifTip | nifShowTip
	d.UCallbackMessage = WM_TRAY
	d.HIcon = t.icon
	copyStr(d.SzTip[:], tip)
	procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&d)))
	d.UVersion = 4
	procShellNotifyIconW.Call(nimSetVersion, uintptr(unsafe.Pointer(&d)))
	t.added = true
}

func (t *Tray) SetTip(tip string) {
	if !t.added || tip == t.tip {
		return
	}
	t.tip = tip
	d := t.data()
	d.UFlags = nifTip | nifShowTip
	copyStr(d.SzTip[:], tip)
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&d)))
}

// Notify shows a Windows notification from the tray icon.
func (t *Tray) Notify(title, text string) {
	if !t.added {
		return
	}
	d := t.data()
	d.UFlags = nifInfo
	copyStr(d.SzInfoTitle[:], title)
	copyStr(d.SzInfo[:], text)
	d.DwInfoFlags = niifUser | niifLargeIc
	d.HBalloonIcon = LoadAppIcon(32)
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&d)))
}

func (t *Tray) Remove() {
	if !t.added {
		return
	}
	d := t.data()
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&d)))
	t.added = false
}

type MenuItem struct {
	ID       int
	Text     string
	Disabled bool
	Sep      bool
}

// ShowMenu pops up a context menu at the cursor and returns the chosen ID (0 = none).
func ShowMenu(hwnd uintptr, items []MenuItem) int {
	m, _, _ := procCreatePopupMenu.Call()
	if m == 0 {
		return 0
	}
	defer procDestroyMenu.Call(m)
	for _, it := range items {
		if it.Sep {
			procAppendMenuW.Call(m, 0x800 /*MF_SEPARATOR*/, 0, 0)
			continue
		}
		flags := uintptr(0)
		if it.Disabled {
			flags |= 0x1 // MF_GRAYED
		}
		procAppendMenuW.Call(m, flags, uintptr(it.ID), uintptr(unsafe.Pointer(wstr(it.Text))))
	}
	var pt struct{ X, Y int32 }
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(hwnd)
	const tpmRightButton, tpmReturnCmd, tpmBottomAlign = 0x2, 0x100, 0x20
	r, _, _ := procTrackPopupMenuEx.Call(m, tpmRightButton|tpmReturnCmd|tpmBottomAlign, uintptr(pt.X), uintptr(pt.Y), hwnd, 0)
	procPostMessageW.Call(hwnd, WM_NULL, 0, 0)
	return int(r)
}

// AllowDarkMenus makes context menus follow the system dark theme
// (undocumented uxtheme ordinals used by many apps; silently skipped if absent).
func AllowDarkMenus() {
	defer func() { _ = recover() }()
	h, err := windows.LoadLibraryEx("uxtheme.dll", 0, windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return
	}
	if p, err := windows.GetProcAddressByOrdinal(h, 135); err == nil {
		syscallN(p, 1) // SetPreferredAppMode(AllowDark)
	}
	if p, err := windows.GetProcAddressByOrdinal(h, 136); err == nil {
		syscallN(p) // FlushMenuThemes
	}
}
