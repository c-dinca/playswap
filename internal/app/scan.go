//go:build windows

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"playswap/internal/core"
	"playswap/internal/win"
)

// scanPC finds games from Steam, Epic, GOG, common game folders on every
// drive, and desktop / Start menu shortcuts.
func (a *App) scanPC() []core.FoundGame {
	env := a.env
	var all []core.FoundGame
	all = append(all, env.ScanSteam()...)

	if pd := win.ProgramData(); pd != "" {
		all = append(all, core.EpicGames(filepath.Join(pd, "Epic", "EpicGamesLauncher", "Data", "Manifests"))...)
	}
	for _, g := range win.GOGGames() {
		exe := g.Exe
		if !filepath.IsAbs(exe) {
			exe = filepath.Join(g.Path, exe)
		}
		if _, err := os.Stat(exe); err == nil {
			all = append(all, core.FoundGame{Key: "dir:" + strings.ToLower(filepath.Clean(g.Path)), Name: g.Name, Source: "gog", Exe: exe, Dir: filepath.Clean(g.Path)})
		}
	}

	// Folders that hold games but are not games themselves.
	home := os.Getenv("USERPROFILE")
	containers := []string{home, win.Desktop(), win.Documents(), win.ProgramFiles(), win.ProgramFilesX86(), win.WindowsDir()}
	var roots []string
	gameDirNames := []string{"Games", "Game", "Jocuri", "Gaming", "Juegos", "Jeux", "Spiele", "Epic Games", "GOG Games",
		`GOG Galaxy\Games`, "PC Games"}
	for _, d := range win.FixedDrives() {
		containers = append(containers, d)
		roots = append(roots, d)
		for _, n := range gameDirNames {
			p := filepath.Join(d, n)
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				roots = append(roots, p)
				containers = append(containers, p)
			}
		}
	}
	for _, pf := range []string{win.ProgramFiles(), win.ProgramFilesX86()} {
		if pf == "" {
			continue
		}
		for _, n := range []string{"Epic Games", "Rockstar Games", "EA Games", "Electronic Arts", "Ubisoft", `Ubisoft\Ubisoft Game Launcher\games`,
			`GOG Galaxy\Games`, "Battle.net", "Riot Games", "Bethesda.net Launcher\\games", "Square Enix", "Games"} {
			p := filepath.Join(pf, n)
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				roots = append(roots, p)
				containers = append(containers, p)
			}
		}
	}
	if home != "" {
		p := filepath.Join(home, "Games")
		if _, err := os.Stat(p); err == nil {
			roots = append(roots, p)
			containers = append(containers, p)
		}
	}
	for _, lib := range core.SteamLibraries(env.SteamPath) {
		containers = append(containers, lib, filepath.Join(lib, "steamapps"), filepath.Join(lib, "steamapps", "common"))
	}
	all = append(all, core.ScanFolders(roots, containers)...)

	// Shortcuts the user made (common for games installed by hand).
	windir := strings.ToLower(win.WindowsDir())
	for lnk, target := range win.ResolveShortcuts(win.ShortcutFiles()) {
		if !strings.EqualFold(filepath.Ext(target), ".exe") || (windir != "" && strings.HasPrefix(strings.ToLower(target), windir)) {
			continue
		}
		if _, err := os.Stat(target); err != nil {
			continue
		}
		base := strings.ToLower(filepath.Base(target))
		if base == "steam.exe" || strings.Contains(base, "launcher") || strings.Contains(base, "unins") || base == "playswap.exe" {
			continue
		}
		root := core.GameRoot(target, containers)
		skip := false
		for _, c := range containers {
			if strings.EqualFold(filepath.Clean(root), filepath.Clean(c)) {
				skip = true
				break
			}
		}
		if skip || !core.LooksLikeGame(root) {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(lnk), filepath.Ext(lnk))
		all = append(all, core.FoundGame{Key: "dir:" + strings.ToLower(filepath.Clean(root)), Name: name, Source: "shortcut", Exe: target, Dir: root})
	}
	return env.FinishFound(all)
}

// visibleFound hides games already in PlaySwap and ones the user dismissed.
func (a *App) visibleFound(list []core.FoundGame) []core.FoundGame {
	a.mu.Lock()
	cfg := a.cfg.Clone()
	a.mu.Unlock()
	ignored := map[string]bool{}
	for _, k := range cfg.IgnoredFound {
		ignored[k] = true
	}
	out := []core.FoundGame{}
	for _, f := range list {
		if ignored[f.Key] || a.isAdded(cfg, f) {
			continue
		}
		out = append(out, f)
	}
	return out
}

func (a *App) isAdded(cfg *core.Config, f core.FoundGame) bool {
	for _, g := range cfg.Games {
		if f.AppID != 0 && g.Launch.Type == "steam" && g.Launch.AppID == f.AppID {
			return true
		}
		if g.Launch.Type == "exe" && f.Dir != "" {
			if exe, err := a.env.ResolveExe(g.Launch.Exe); err == nil {
				if rel, err := filepath.Rel(strings.ToLower(f.Dir), strings.ToLower(exe)); err == nil && !strings.HasPrefix(rel, "..") {
					return true
				}
			}
		}
	}
	return false
}

func (a *App) startScan() {
	a.mu.Lock()
	if a.scanning {
		a.mu.Unlock()
		return
	}
	a.scanning = true
	a.mu.Unlock()
	a.emit("found", map[string]any{"scanning": true})
	go func() {
		start := time.Now()
		var list []core.FoundGame
		func() {
			defer func() {
				if r := recover(); r != nil {
					a.logf("ERROR scan crashed: %v", r)
				}
			}()
			list = a.scanPC()
		}()
		a.mu.Lock()
		a.found = list
		a.scanning = false
		a.mu.Unlock()
		a.logf("Scan found %d games in %s", len(list), time.Since(start).Round(100*time.Millisecond))
		a.emit("found", map[string]any{"scanning": false})
	}()
}

type FoundState struct {
	Scanning bool             `json:"scanning"`
	Games    []core.FoundGame `json:"games"`
}

func (a *App) apiFound() FoundState {
	a.mu.Lock()
	list := append([]core.FoundGame{}, a.found...)
	scanning := a.scanning
	a.mu.Unlock()
	return FoundState{Scanning: scanning, Games: a.visibleFound(list)}
}

func (a *App) apiScan() { a.startScan() }

func (a *App) apiIgnoreFound(key string) error {
	a.mu.Lock()
	cfg := a.cfg.Clone()
	a.mu.Unlock()
	cfg.IgnoredFound = append(cfg.IgnoredFound, key)
	return a.saveConfig(cfg)
}

type AddResult struct {
	Added  []string `json:"added"`
	Failed []string `json:"failed"`
	Steam  int      `json:"steam"`
}

// gameFromFound turns a scan result into a game definition.
func gameFromFound(f core.FoundGame) core.Game {
	g := core.Game{Name: f.Name, ProcessName: f.ProcessName, PlayerArgs: map[string]string{}}
	if f.Source == "steam" {
		g.Launch = core.Launch{Type: "steam", AppID: f.AppID}
	} else {
		g.Launch = core.Launch{Type: "exe", Exe: f.Exe}
	}
	for _, p := range f.Paths {
		g.Paths = append(g.Paths, core.PathEntry{Path: p})
	}
	return g
}

func (a *App) apiFoundGame(key string) (core.FoundGame, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, f := range a.found {
		if f.Key == key {
			return f, nil
		}
	}
	return core.FoundGame{}, fmt.Errorf("That game isn't in the scan results anymore; scan again.")
}

// apiAddFound adds the given scan results that have everything they need.
func (a *App) apiAddFound(keys []string) (AddResult, error) {
	res := AddResult{Added: []string{}, Failed: []string{}}
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	a.mu.Lock()
	found := append([]core.FoundGame{}, a.found...)
	a.mu.Unlock()
	for _, f := range found {
		if !want[f.Key] {
			continue
		}
		if !f.Ready {
			res.Failed = append(res.Failed, f.Name+": needs a save location")
			continue
		}
		g, err := a.apiSaveGameQuiet(gameFromFound(f))
		if err != nil {
			res.Failed = append(res.Failed, f.Name+": "+err.Error())
			continue
		}
		res.Added = append(res.Added, g.Name)
		if g.Launch.Type == "steam" {
			res.Steam++
		}
	}
	if res.Steam > 0 {
		a.autoHooks()
	}
	return res, nil
}
