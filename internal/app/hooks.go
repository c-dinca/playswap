//go:build windows

package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"playswap/internal/core"
	"playswap/internal/win"
)

// ErrSteamRunning means Steam must close before its launch options can be
// written (Steam rewrites localconfig.vdf while it runs and when it exits).
var ErrSteamRunning = errors.New("steam-running")

func steamRunning() bool {
	procs, err := win.ProcessNames()
	return err == nil && procs["steam"]
}

func shutdownSteam(steamPath string) error {
	if !steamRunning() {
		return nil
	}
	_ = win.StartDetached(filepath.Join(steamPath, "steam.exe"), "-shutdown", steamPath)
	for i := 0; i < 90; i++ { // up to 45 s
		time.Sleep(500 * time.Millisecond)
		if !steamRunning() {
			time.Sleep(1500 * time.Millisecond) // let it finish writing its files
			return nil
		}
	}
	return errors.New("Steam didn't close. Close it yourself (Steam menu, Exit), then try again.")
}

type hookChange struct {
	appID int
	value string
	name  string
}

// planHooks works out the launch option values to write. install=false puts
// every game's original options back (used by uninstall).
func planHooks(cfg *core.Config, text, userDir, exe string, install bool) ([]hookChange, error) {
	var out []hookChange
	if text != "" {
		if _, err := core.ParseVDF(text); err != nil {
			return nil, fmt.Errorf("Steam's settings file couldn't be read: %w", err)
		}
	}
	cur := func(appID int) string {
		v, _ := core.LaunchOptionsFromText(text, appID)
		return v
	}
	for i := range cfg.Games {
		g := &cfg.Games[i]
		if g.Launch.Type != "steam" {
			continue
		}
		now := cur(g.Launch.AppID)
		has := core.HasWrapper(now, g.Launch.AppID)
		if install {
			if has && strings.Contains(strings.ToLower(now), strings.ToLower(exe)) {
				continue // already pointing at this PlaySwap
			}
			prev := now
			if has {
				prev = g.SteamPrevOptions // wrapper from an older install location
			} else {
				g.SteamPrevOptions = now
			}
			out = append(out, hookChange{g.Launch.AppID, core.WrapperLaunchOption(exe, g.Launch.AppID, prev), g.Name})
		} else if has {
			out = append(out, hookChange{g.Launch.AppID, g.SteamPrevOptions, g.Name})
		}
	}
	for idStr, prev := range cfg.SteamRestores {
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		if cfg.Game(findSteamGameID(cfg, id)) != nil && install {
			continue // the game was added back
		}
		if core.HasWrapper(cur(id), id) {
			out = append(out, hookChange{id, prev, "app " + idStr})
		}
	}
	return out, nil
}

func findSteamGameID(cfg *core.Config, appID int) string {
	for _, g := range cfg.Games {
		if g.Launch.Type == "steam" && g.Launch.AppID == appID {
			return g.ID
		}
	}
	return ""
}

type hookResult struct {
	Changed      int    `json:"changed"`
	NeedsRestart bool   `json:"needsRestart"`
	Restarted    bool   `json:"restarted"`
	Message      string `json:"message,omitempty"`
}

// syncSteamHooks writes (install) or removes (uninstall) PlaySwap's Steam
// launch options. With allowRestart it closes Steam, edits, and reopens it;
// without, it returns NeedsRestart when Steam is running. cfg is updated
// (original options, finished restores); the caller saves it.
func syncSteamHooks(cfg *core.Config, env *core.Env, exe string, install, allowRestart bool, logf func(string, ...any)) (hookResult, error) {
	var res hookResult
	if env.SteamPath == "" {
		return res, nil
	}
	userDir, err := env.SteamUser()
	if err != nil {
		return res, err
	}
	path := filepath.Join(userDir, "config", "localconfig.vdf")
	read := func() string {
		b, _ := os.ReadFile(path)
		return string(b)
	}

	// Anything to do at all?
	probe := cfg.Clone()
	changes, err := planHooks(probe, read(), userDir, exe, install)
	if err != nil {
		return res, err
	}
	if len(changes) == 0 {
		if install {
			cfg.SteamRestores = nil
		}
		return res, nil
	}

	wasRunning := steamRunning()
	if wasRunning {
		if !allowRestart {
			res.NeedsRestart = true
			res.Changed = len(changes)
			return res, ErrSteamRunning
		}
		logf("Closing Steam to update launch options")
		if err := shutdownSteam(env.SteamPath); err != nil {
			return res, err
		}
	}

	// Steam may have rewritten the file while closing: plan again from disk.
	text := read()
	changes, err = planHooks(cfg, text, userDir, exe, install)
	if err != nil {
		return res, err
	}
	out := text
	for _, c := range changes {
		out, err = core.SetVDFValue(out, core.SteamAppsPath(c.appID), "LaunchOptions", c.value)
		if err != nil {
			return res, fmt.Errorf("couldn't update Steam's settings file: %w", err)
		}
	}
	if out != text {
		if text != "" {
			_ = os.WriteFile(path+".playswap-backup", []byte(text), 0o644)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return res, err
		}
		if err := os.WriteFile(path+".playswap-tmp", []byte(out), 0o644); err != nil {
			return res, err
		}
		if err := os.Rename(path+".playswap-tmp", path); err != nil {
			return res, err
		}
	}
	for _, c := range changes {
		if install {
			logf("Steam launch options set for %s", c.name)
		} else {
			logf("Steam launch options restored for %s", c.name)
		}
	}
	if install {
		cfg.SteamRestores = nil
	}
	res.Changed = len(changes)
	if wasRunning {
		_ = win.StartDetached(filepath.Join(env.SteamPath, "steam.exe"), "", env.SteamPath)
		res.Restarted = true
	}
	return res, nil
}

// applyHooks is the app-side wrapper: serialises runs and saves config.
// restoresOnly skips adding PlaySwap to games (used when the automatic
// setting is off but a removed game's options still need putting back).
func (a *App) applyHooks(allowRestart, restoresOnly bool) (hookResult, error) {
	a.hookMu.Lock()
	defer a.hookMu.Unlock()
	a.mu.Lock()
	cfg := a.cfg.Clone()
	a.mu.Unlock()
	if restoresOnly {
		cfg.Games = nil
	}
	res, err := syncSteamHooks(cfg, a.env, a.exePath, true, allowRestart, a.logf)
	if err == nil && res.Changed > 0 {
		// merge only the hook fields into the live config
		a.mu.Lock()
		live := a.cfg.Clone()
		live.SteamRestores = cfg.SteamRestores
		for _, g := range cfg.Games {
			if lg := live.Game(g.ID); lg != nil {
				lg.SteamPrevOptions = g.SteamPrevOptions
			}
		}
		a.mu.Unlock()
		if serr := a.saveConfig(live); serr != nil {
			return res, serr
		}
	}
	a.mu.Lock()
	a.hooksPending = errors.Is(err, ErrSteamRunning)
	a.mu.Unlock()
	if res.Changed > 0 && err == nil {
		a.emit("hooks", nil)
	}
	return res, err
}

// autoHooks applies silently if Steam is closed; otherwise it remembers to
// try again (the watcher retries) and tells the UI.
func (a *App) autoHooks() {
	a.mu.Lock()
	on := a.cfg.Settings.AutoSteamHooks
	a.mu.Unlock()
	if !on {
		return
	}
	go func() {
		res, err := a.applyHooks(false, false)
		switch {
		case errors.Is(err, ErrSteamRunning):
			a.emit("hooks", map[string]any{"needsRestart": true, "count": res.Changed})
		case err != nil:
			a.logf("ERROR Steam launch options: %v", err)
		}
	}()
}

// autoHooksAlways is used when removing a game: taking PlaySwap back out of
// Steam isn't optional, whatever the auto setting says.
func (a *App) autoHooksAlways() {
	a.mu.Lock()
	restoresOnly := !a.cfg.Settings.AutoSteamHooks
	a.mu.Unlock()
	go func() {
		res, err := a.applyHooks(false, restoresOnly)
		if errors.Is(err, ErrSteamRunning) {
			a.emit("hooks", map[string]any{"needsRestart": true, "count": res.Changed})
		} else if err != nil {
			a.logf("ERROR Steam launch options: %v", err)
		}
	}()
}

func (a *App) apiApplyHooks(allowRestart bool) (hookResult, error) {
	res, err := a.applyHooks(allowRestart, false)
	if errors.Is(err, ErrSteamRunning) {
		return res, nil
	}
	return res, err
}

// restoreHooksForUninstall puts every game's original launch options back.
func restoreHooksForUninstall(quiet bool) {
	store := &core.Store{Dir: DataDir()}
	cfg, first, err := store.LoadConfig()
	if err != nil || first {
		return
	}
	env := newEnv(nil)
	env.SteamUser = func() (string, error) {
		_, active := win.SteamInfo()
		return core.SteamUserDir(env.SteamPath, cfg.Settings.SteamAccountID, active)
	}
	exe, _ := os.Executable()
	logf := func(string, ...any) {}
	_, err = syncSteamHooks(cfg, env, exe, false, false, logf)
	if errors.Is(err, ErrSteamRunning) {
		ok := quiet || win.MessageBox(0, "PlaySwap needs to close Steam for a few seconds to take its launch options back out of your games. Steam reopens afterwards.\n\nClose Steam now?",
			Name+" uninstall", win.MB_YESNO|win.MB_ICONQUESTION) == win.IDYES
		if ok {
			_, err = syncSteamHooks(cfg, env, exe, false, true, logf)
		}
	}
	if err != nil && !quiet {
		var names []string
		for _, g := range cfg.Games {
			if g.Launch.Type == "steam" {
				names = append(names, g.Name)
			}
		}
		win.MessageBox(0, "Couldn't remove PlaySwap from Steam's launch options automatically.\n\nIn Steam, open Properties for these games and clear their Launch options:\n"+strings.Join(names, "\n"),
			Name+" uninstall", win.MB_ICONWARNING)
	}
}
