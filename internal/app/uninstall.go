//go:build windows

package app

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"playswap/internal/core"
	"playswap/internal/win"
)

// StopRunning asks a running PlaySwap to quit and waits for it to go away.
func StopRunning() bool {
	h := win.FindWindow(WindowClass)
	if h == 0 {
		return true
	}
	win.SendCommand(h, "quit-now")
	for i := 0; i < 50; i++ {
		if win.FindWindow(WindowClass) == 0 {
			time.Sleep(300 * time.Millisecond) // let the process exit fully
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return win.FindWindow(WindowClass) == 0
}

// StartMenuShortcut is where the installer puts PlaySwap's Start menu entry.
func StartMenuShortcut() string {
	return filepath.Join(win.StartMenuPrograms(), Name+".lnk")
}

func DesktopShortcut() string {
	return filepath.Join(win.Desktop(), Name+".lnk")
}

// Uninstall removes PlaySwap from this PC (run as "PlaySwap.exe --uninstall").
func Uninstall(quiet bool) {
	if !quiet && win.MessageBox(0, "Remove PlaySwap from this PC?", Name+" uninstall", win.MB_YESNO|win.MB_ICONQUESTION) != win.IDYES {
		return
	}
	if !StopRunning() {
		win.MessageBox(0, "PlaySwap is still running and didn't close. Quit it from the tray icon, then try again.", Name+" uninstall", win.MB_ICONWARNING)
		return
	}

	deleteData := false
	if !quiet {
		msg := "Also delete everyone's saved profiles and backups?\n\n" +
			"Yes: delete them.\nNo: keep them in " + DataDir() + " (PlaySwap picks them up again if you reinstall).\n\n" +
			"Either way, each game's folders keep the files of whoever played last."
		deleteData = win.MessageBox(0, msg, Name+" uninstall", win.MB_YESNO|win.MB_ICONQUESTION|win.MB_DEFBUTTON2) == win.IDYES
	}

	// Take PlaySwap back out of Steam's launch options, or games wouldn't start.
	restoreHooksForUninstall(quiet)

	// Shortcuts PlaySwap created on the desktop for games.
	store := &core.Store{Dir: DataDir()}
	if cfg, first, err := store.LoadConfig(); err == nil && !first {
		for _, s := range cfg.Shortcuts {
			if strings.HasSuffix(strings.ToLower(s), " (playswap).lnk") {
				_ = os.Remove(s)
			}
		}
	}
	_ = os.Remove(StartMenuShortcut())
	_ = os.Remove(DesktopShortcut())
	_ = win.SetAutostart(Name, "", false)
	win.UnregisterUninstall(UninstallID)

	if deleteData {
		_ = os.RemoveAll(DataDir())
	} else {
		_ = os.RemoveAll(filepath.Join(DataDir(), "WebView2"))
	}

	if !quiet {
		win.MessageBox(0, "PlaySwap was removed.", Name+" uninstall", win.MB_ICONINFORMATION)
	}
	// Remove the program folder right after this process exits.
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	if strings.EqualFold(filepath.Base(dir), Name) && strings.EqualFold(filepath.Base(exe), Name+".exe") {
		cmd := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
		_ = win.StartHidden(cmd, `/d /c ping -n 3 127.0.0.1 >nul & rmdir /s /q "`+dir+`"`)
	}
}
