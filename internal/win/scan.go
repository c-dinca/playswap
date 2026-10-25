//go:build windows

package win

import (
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// FixedDrives returns local hard-drive roots like C:\ and D:\.
func FixedDrives() []string {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	var out []string
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		if windows.GetDriveType(windows.StringToUTF16Ptr(root)) == windows.DRIVE_FIXED {
			out = append(out, root)
		}
	}
	return out
}

type GOGGame struct{ Name, Path, Exe string }

// GOGGames lists games installed through GOG (from the registry).
func GOGGames() []GOGGame {
	var out []GOGGame
	for _, sub := range []string{`SOFTWARE\WOW6432Node\GOG.com\Games`, `SOFTWARE\GOG.com\Games`} {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, sub, registry.ENUMERATE_SUB_KEYS)
		if err != nil {
			continue
		}
		names, _ := k.ReadSubKeyNames(-1)
		k.Close()
		for _, n := range names {
			g, err := registry.OpenKey(registry.LOCAL_MACHINE, sub+`\`+n, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			name, _, _ := g.GetStringValue("gameName")
			path, _, _ := g.GetStringValue("path")
			exe, _, _ := g.GetStringValue("exe")
			g.Close()
			if name != "" && path != "" && exe != "" {
				out = append(out, GOGGame{name, path, exe})
			}
		}
		if len(out) > 0 {
			break
		}
	}
	return out
}

// ShortcutFiles lists .lnk files on the desktops and in the Start menus.
func ShortcutFiles() []string {
	var dirs []string
	for _, id := range []*windows.KNOWNFOLDERID{windows.FOLDERID_Desktop, windows.FOLDERID_PublicDesktop, windows.FOLDERID_Programs, windows.FOLDERID_CommonPrograms} {
		if p := KnownFolder(id); p != "" {
			dirs = append(dirs, p)
		}
	}
	var out []string
	for _, d := range dirs {
		base := strings.Count(d, `\`)
		_ = filepath.WalkDir(d, func(p string, e os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if e.IsDir() && strings.Count(p, `\`)-base > 2 {
				return filepath.SkipDir
			}
			if !e.IsDir() && strings.EqualFold(filepath.Ext(p), ".lnk") {
				out = append(out, p)
			}
			return nil
		})
	}
	return out
}

// ResolveShortcuts returns each .lnk's target path (only those that resolve).
func ResolveShortcuts(lnks []string) map[string]string {
	out := map[string]string{}
	_ = withCOM(func() error {
		for _, l := range lnks {
			link, err := createInstance(clsidShellLink, iidShellLinkW)
			if err != nil {
				return err
			}
			var pf *comObj
			if hr(link.call(0, uintptr(unsafe.Pointer(iidPersistFile)), uintptr(unsafe.Pointer(&pf)))) == nil {
				// IPersistFile::Load(path, STGM_READ)
				if hr(pf.call(5, uintptr(unsafe.Pointer(wstr(l))), 0)) == nil {
					buf := make([]uint16, 1024)
					// IShellLinkW::GetPath(buf, len, NULL, SLGP_RAWPATH=4)
					if hr(link.call(3, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0, 4)) == nil {
						t := windows.UTF16ToString(buf)
						if t != "" {
							if exp, err := registry.ExpandString(t); err == nil {
								t = exp
							}
							out[l] = t
						}
					}
				}
				pf.release()
			}
			link.release()
		}
		return nil
	})
	return out
}

func ProgramData() string { return KnownFolder(windows.FOLDERID_ProgramData) }
