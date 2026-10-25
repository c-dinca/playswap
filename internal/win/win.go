//go:build windows

// Package win wraps the Win32 / COM calls PlaySwap needs.
package win

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	ole32    = windows.NewLazySystemDLL("ole32.dll")
	dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procMessageBoxW                   = user32.NewProc("MessageBoxW")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procGetDpiForWindow               = user32.NewProc("GetDpiForWindow")
	procGetDpiForSystem               = user32.NewProc("GetDpiForSystem")
	procShowWindow                    = user32.NewProc("ShowWindow")
	procSetForegroundWindow           = user32.NewProc("SetForegroundWindow")
	procIsWindowVisible               = user32.NewProc("IsWindowVisible")
	procIsIconic                      = user32.NewProc("IsIconic")
	procFindWindowW                   = user32.NewProc("FindWindowW")
	procSendMessageTimeoutW           = user32.NewProc("SendMessageTimeoutW")
	procPostMessageW                  = user32.NewProc("PostMessageW")
	procAllowSetForegroundWindow      = user32.NewProc("AllowSetForegroundWindow")
	procGetWindowThreadProcessId      = user32.NewProc("GetWindowThreadProcessId")
	procLoadImageW                    = user32.NewProc("LoadImageW")
	procSendMessageW                  = user32.NewProc("SendMessageW")
	procGetSystemMetricsForDpi        = user32.NewProc("GetSystemMetricsForDpi")
	procGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	procSetWindowPos                  = user32.NewProc("SetWindowPos")
	procRegisterWindowMessageW        = user32.NewProc("RegisterWindowMessageW")
	procCreatePopupMenu               = user32.NewProc("CreatePopupMenu")
	procAppendMenuW                   = user32.NewProc("AppendMenuW")
	procTrackPopupMenuEx              = user32.NewProc("TrackPopupMenuEx")
	procDestroyMenu                   = user32.NewProc("DestroyMenu")
	procGetCursorPos                  = user32.NewProc("GetCursorPos")
	procBringWindowToTop              = user32.NewProc("BringWindowToTop")
	procShellNotifyIconW              = shell32.NewProc("Shell_NotifyIconW")
	procSHCreateItemFromParsingName   = shell32.NewProc("SHCreateItemFromParsingName")
	procCoCreateInstance              = ole32.NewProc("CoCreateInstance")
	procCoInitializeEx                = ole32.NewProc("CoInitializeEx")
	procCoTaskMemFree                 = ole32.NewProc("CoTaskMemFree")
	procDwmSetWindowAttribute         = dwmapi.NewProc("DwmSetWindowAttribute")
)

const (
	MB_OK              = 0x0
	MB_OKCANCEL        = 0x1
	MB_YESNOCANCEL     = 0x3
	MB_YESNO           = 0x4
	MB_ICONERROR       = 0x10
	MB_ICONQUESTION    = 0x20
	MB_ICONWARNING     = 0x30
	MB_ICONINFORMATION = 0x40
	MB_DEFBUTTON2      = 0x100
	MB_SETFOREGROUND   = 0x10000
	MB_TOPMOST         = 0x40000

	IDOK     = 1
	IDCANCEL = 2
	IDYES    = 6
	IDNO     = 7

	SW_HIDE    = 0
	SW_SHOW    = 5
	SW_RESTORE = 9

	WM_NULL          = 0x0000
	WM_CLOSE         = 0x0010
	WM_SETICON       = 0x0080
	WM_COPYDATA      = 0x004A
	WM_CONTEXTMENU   = 0x007B
	WM_DPICHANGED    = 0x02E0
	WM_LBUTTONUP     = 0x0202
	WM_LBUTTONDBLCLK = 0x0203
	WM_RBUTTONUP     = 0x0205
	WM_APP           = 0x8000
)

// MessageBox shows a native message box and returns the button pressed.
func MessageBox(hwnd uintptr, text, caption string, flags uint32) int {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString(caption)
	r, _, _ := procMessageBoxW.Call(hwnd, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), uintptr(flags|MB_SETFOREGROUND))
	return int(r)
}

// EnableDPIAwareness opts into per-monitor DPI v2 so text stays sharp.
func EnableDPIAwareness() {
	if procSetProcessDpiAwarenessContext.Find() == nil {
		const perMonitorV2 = ^uintptr(3) // (DPI_AWARENESS_CONTEXT)-4
		procSetProcessDpiAwarenessContext.Call(perMonitorV2)
	}
}

func SystemDPI() int {
	if procGetDpiForSystem.Find() == nil {
		r, _, _ := procGetDpiForSystem.Call()
		if r > 0 {
			return int(r)
		}
	}
	return 96
}

func WindowDPI(hwnd uintptr) int {
	if procGetDpiForWindow.Find() == nil {
		r, _, _ := procGetDpiForWindow.Call(hwnd)
		if r > 0 {
			return int(r)
		}
	}
	return SystemDPI()
}

func ShowWindow(hwnd uintptr, cmd int) { procShowWindow.Call(hwnd, uintptr(cmd)) }

func IsVisible(hwnd uintptr) bool {
	r, _, _ := procIsWindowVisible.Call(hwnd)
	return r != 0
}

// Present shows, restores and focuses a window.
func Present(hwnd uintptr) {
	if r, _, _ := procIsIconic.Call(hwnd); r != 0 {
		procShowWindow.Call(hwnd, SW_RESTORE)
	} else {
		procShowWindow.Call(hwnd, SW_SHOW)
	}
	procBringWindowToTop.Call(hwnd)
	procSetForegroundWindow.Call(hwnd)
}

func SetForeground(hwnd uintptr) { procSetForegroundWindow.Call(hwnd) }

func PostMessage(hwnd uintptr, msg uint32, wp, lp uintptr) {
	procPostMessageW.Call(hwnd, uintptr(msg), wp, lp)
}

// ApplyDPIRect moves a window to the rectangle suggested by WM_DPICHANGED.
func ApplyDPIRect(hwnd, lp uintptr) {
	r := (*[4]int32)(unsafe.Pointer(lp))
	const swpNoZOrder, swpNoActivate = 0x4, 0x10
	procSetWindowPos.Call(hwnd, 0, uintptr(r[0]), uintptr(r[1]), uintptr(r[2]-r[0]), uintptr(r[3]-r[1]), swpNoZOrder|swpNoActivate)
}

func Center(hwnd uintptr, w, h int) {
	sw, _, _ := procGetSystemMetrics.Call(0) // SM_CXSCREEN
	sh, _, _ := procGetSystemMetrics.Call(1)
	x, y := (int(sw)-w)/2, (int(sh)-h)/2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	const swpNoZOrder, swpNoActivate = 0x4, 0x10
	procSetWindowPos.Call(hwnd, 0, uintptr(x), uintptr(y), uintptr(w), uintptr(h), swpNoZOrder|swpNoActivate)
}

// StyleTitleBar asks DWM for a dark title bar and, on Windows 11, paints the
// caption in the app's own background colour (0xRRGGBB).
func StyleTitleBar(hwnd uintptr, rgb uint32) {
	if procDwmSetWindowAttribute.Find() != nil {
		return
	}
	on := int32(1)
	procDwmSetWindowAttribute.Call(hwnd, 20, uintptr(unsafe.Pointer(&on)), 4) // DWMWA_USE_IMMERSIVE_DARK_MODE
	colorref := uint32((rgb&0xFF)<<16 | (rgb & 0xFF00) | (rgb>>16)&0xFF)
	procDwmSetWindowAttribute.Call(hwnd, 35, uintptr(unsafe.Pointer(&colorref)), 4) // DWMWA_CAPTION_COLOR
	text := uint32(0x00F2EDEA)
	procDwmSetWindowAttribute.Call(hwnd, 36, uintptr(unsafe.Pointer(&text)), 4) // DWMWA_TEXT_COLOR
}

// LoadAppIcon loads icon resource 1 from the exe at the given pixel size.
func LoadAppIcon(size int) uintptr {
	hinst, _ := getModuleHandle()
	h, _, _ := procLoadImageW.Call(hinst, 1, 1 /*IMAGE_ICON*/, uintptr(size), uintptr(size), 0)
	return h
}

func getModuleHandle() (uintptr, error) {
	var h windows.Handle
	err := windows.GetModuleHandleEx(0, nil, &h)
	return uintptr(h), err
}

func SmallIconSize(dpi int) int {
	if procGetSystemMetricsForDpi.Find() == nil {
		r, _, _ := procGetSystemMetricsForDpi.Call(49, uintptr(dpi)) // SM_CXSMICON
		if r > 0 {
			return int(r)
		}
	}
	return 16 * dpi / 96
}

func SetWindowIcons(hwnd uintptr, dpi int) {
	small := SmallIconSize(dpi)
	big := 32 * dpi / 96
	if h := LoadAppIcon(small); h != 0 {
		procSendMessageW.Call(hwnd, WM_SETICON, 0, h)
	}
	if h := LoadAppIcon(big); h != 0 {
		procSendMessageW.Call(hwnd, WM_SETICON, 1, h)
	}
}

func RegisterWindowMessage(name string) uint32 {
	p, _ := windows.UTF16PtrFromString(name)
	r, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(p)))
	return uint32(r)
}

// ------------------------------------------------------- single instance --

// AcquireSingleInstance returns false if another PlaySwap is already running.
func AcquireSingleInstance(name string) bool {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return true
	}
	h, err := windows.CreateMutex(nil, false, p)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return false
	}
	// On success the handle is deliberately kept open for the life of the process.
	return true
}

func FindWindow(class string) uintptr {
	c, _ := windows.UTF16PtrFromString(class)
	h, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(c)), 0)
	return h
}

type copyDataStruct struct {
	dwData uintptr
	cbData uint32
	lpData uintptr
}

// SendCommand delivers a text command to another window via WM_COPYDATA and
// lets it take the foreground.
func SendCommand(hwnd uintptr, cmd string) bool {
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid != 0 {
		procAllowSetForegroundWindow.Call(uintptr(pid))
	}
	b := []byte(cmd)
	if len(b) == 0 {
		b = []byte{0}
	}
	cds := copyDataStruct{dwData: 0x50535750, cbData: uint32(len(b)), lpData: uintptr(unsafe.Pointer(&b[0]))}
	var result uintptr
	r, _, _ := procSendMessageTimeoutW.Call(hwnd, WM_COPYDATA, 0, uintptr(unsafe.Pointer(&cds)), 0x2 /*SMTO_ABORTIFHUNG*/, 5000, uintptr(unsafe.Pointer(&result)))
	runtime.KeepAlive(b)
	return r != 0
}

// ReadCommand decodes a WM_COPYDATA lParam sent by SendCommand.
func ReadCommand(lp uintptr) (string, bool) {
	cds := (*copyDataStruct)(unsafe.Pointer(lp))
	if cds == nil || cds.dwData != 0x50535750 || cds.cbData == 0 || cds.cbData > 4096 {
		return "", false
	}
	b := unsafe.Slice((*byte)(unsafe.Pointer(cds.lpData)), cds.cbData)
	return strings.TrimRight(string(b), "\x00"), true
}

// ------------------------------------------------------------- processes --

// ProcessNames returns the lower-case names (without .exe) of running processes.
func ProcessNames() (map[string]bool, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	out := map[string]bool{}
	if err := windows.Process32First(snap, &e); err != nil {
		return out, nil
	}
	for {
		name := strings.ToLower(windows.UTF16ToString(e.ExeFile[:]))
		out[strings.TrimSuffix(name, ".exe")] = true
		if err := windows.Process32Next(snap, &e); err != nil {
			break
		}
	}
	return out, nil
}

// StartDetached runs exe with a raw argument string and forgets about it.
func StartDetached(exe, args, dir string) error {
	cmd := exec.Command(exe)
	cmd.Dir = dir
	line := syscall.EscapeArg(exe)
	if strings.TrimSpace(args) != "" {
		line += " " + args
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// StartHidden runs a console command without a window (used by uninstall).
func StartHidden(exe, args string) error {
	cmd := exec.Command(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: syscall.EscapeArg(exe) + " " + args, HideWindow: true, CreationFlags: 0x08000000 /*CREATE_NO_WINDOW*/}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// ShellOpen opens a folder, file or https URL with its default handler.
func ShellOpen(target string) error {
	return windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr(target), nil, nil, windows.SW_SHOWNORMAL)
}

// RevealInExplorer opens Explorer with the item selected.
func RevealInExplorer(p string) error {
	return StartDetached(filepath.Join(os.Getenv("SystemRoot"), "explorer.exe"), "/select,"+syscall.EscapeArg(p), "")
}

// ---------------------------------------------------------------- folders --

func KnownFolder(id *windows.KNOWNFOLDERID) string {
	p, err := windows.KnownFolderPath(id, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return ""
	}
	return p
}

func Documents() string  { return KnownFolder(windows.FOLDERID_Documents) }
func SavedGames() string { return KnownFolder(windows.FOLDERID_SavedGames) }
func LocalLow() string   { return KnownFolder(windows.FOLDERID_LocalAppDataLow) }
func Desktop() string    { return KnownFolder(windows.FOLDERID_Desktop) }
func StartMenuPrograms() string {
	return KnownFolder(windows.FOLDERID_Programs)
}
func ProgramFiles() string    { return KnownFolder(windows.FOLDERID_ProgramFiles) }
func ProgramFilesX86() string { return KnownFolder(windows.FOLDERID_ProgramFilesX86) }
func WindowsDir() string      { return KnownFolder(windows.FOLDERID_Windows) }

// --------------------------------------------------------------- registry --

// SteamInfo returns Steam's install folder and the account ID of the user
// currently signed in (0 if Steam isn't running).
func SteamInfo() (path string, activeUser uint32) {
	if k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Valve\Steam`, registry.QUERY_VALUE); err == nil {
		if v, _, err := k.GetStringValue("SteamPath"); err == nil {
			path = filepath.Clean(strings.ReplaceAll(v, "/", `\`))
		}
		k.Close()
	}
	if path == "" {
		for _, root := range []registry.Key{registry.LOCAL_MACHINE} {
			for _, sub := range []string{`SOFTWARE\WOW6432Node\Valve\Steam`, `SOFTWARE\Valve\Steam`} {
				if k, err := registry.OpenKey(root, sub, registry.QUERY_VALUE); err == nil {
					if v, _, err := k.GetStringValue("InstallPath"); err == nil {
						path = filepath.Clean(v)
					}
					k.Close()
				}
				if path != "" {
					break
				}
			}
		}
	}
	if k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Valve\Steam\ActiveProcess`, registry.QUERY_VALUE); err == nil {
		if v, _, err := k.GetIntegerValue("ActiveUser"); err == nil {
			activeUser = uint32(v)
		}
		k.Close()
	}
	if path != "" {
		if _, err := os.Stat(filepath.Join(path, "steam.exe")); err != nil {
			path = ""
		}
	}
	return
}

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func SetAutostart(name, command string, on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if on {
		return k.SetStringValue(name, command)
	}
	err = k.DeleteValue(name)
	if errors.Is(err, registry.ErrNotExist) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return nil
	}
	return err
}

func Autostart(name string) (string, bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	return v, err == nil && v != ""
}

const uninstallRoot = `Software\Microsoft\Windows\CurrentVersion\Uninstall\`

type UninstallEntry struct {
	DisplayName, DisplayVersion, Publisher, DisplayIcon, InstallLocation, UninstallString, QuietUninstallString string
	EstimatedSizeKB                                                                                             uint32
}

func RegisterUninstall(id string, e UninstallEntry) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, uninstallRoot+id, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	for name, v := range map[string]string{
		"DisplayName": e.DisplayName, "DisplayVersion": e.DisplayVersion, "Publisher": e.Publisher,
		"DisplayIcon": e.DisplayIcon, "InstallLocation": e.InstallLocation, "UninstallString": e.UninstallString,
		"QuietUninstallString": e.QuietUninstallString,
	} {
		if v != "" {
			if err := k.SetStringValue(name, v); err != nil {
				return err
			}
		}
	}
	_ = k.SetDWordValue("EstimatedSize", e.EstimatedSizeKB)
	_ = k.SetDWordValue("NoModify", 1)
	_ = k.SetDWordValue("NoRepair", 1)
	return nil
}

func UnregisterUninstall(id string) {
	_ = registry.DeleteKey(registry.CURRENT_USER, uninstallRoot+id)
}

func InstalledLocation(id string) string {
	k, err := registry.OpenKey(registry.CURRENT_USER, uninstallRoot+id, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, _ := k.GetStringValue("InstallLocation")
	return v
}

// WebView2Installed checks the Evergreen runtime registration (per-machine or per-user).
func WebView2Installed() bool {
	const client = `{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`
	check := func(root registry.Key, path string) bool {
		k, err := registry.OpenKey(root, path, registry.QUERY_VALUE|registry.WOW64_32KEY)
		if err != nil {
			return false
		}
		defer k.Close()
		v, _, err := k.GetStringValue("pv")
		return err == nil && v != "" && v != "0.0.0.0"
	}
	return check(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\EdgeUpdate\Clients\`+client) ||
		check(registry.CURRENT_USER, `Software\Microsoft\EdgeUpdate\Clients\`+client)
}
