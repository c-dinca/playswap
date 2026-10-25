//go:build windows

package app

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"playswap/internal/core"
	"playswap/internal/win"
)

// bind exposes the API to the page as window.ps_<name>(...) promises.
func (a *App) bind() {
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	must(a.w.Bind("ps_ready", a.apiReady))
	must(a.w.Bind("ps_state", a.apiState))
	must(a.w.Bind("ps_savePlayers", a.apiSavePlayers))
	must(a.w.Bind("ps_saveGame", a.apiSaveGame))
	must(a.w.Bind("ps_deleteGame", a.apiDeleteGame))
	must(a.w.Bind("ps_saveSettings", a.apiSaveSettings))
	must(a.w.Bind("ps_steamGames", a.apiSteamGames))
	must(a.w.Bind("ps_inspectSteam", a.apiInspectSteam))
	must(a.w.Bind("ps_inspectExe", a.apiInspectExe))
	must(a.w.Bind("ps_checkPath", a.apiCheckPath))
	must(a.w.Bind("ps_browse", a.apiBrowse))
	must(a.w.Bind("ps_play", a.apiPlay))
	must(a.w.Bind("ps_load", a.apiLoad))
	must(a.w.Bind("ps_shortcut", a.apiShortcut))
	must(a.w.Bind("ps_open", a.apiOpen))
	must(a.w.Bind("ps_log", a.apiLog))
	must(a.w.Bind("ps_steamSetup", a.apiSteamSetup))
	must(a.w.Bind("ps_copy", a.apiCopy))
	must(a.w.Bind("ps_steamPick", a.apiSteamPick))
	must(a.w.Bind("ps_cancelRequest", a.apiCancelRequest))
	must(a.w.Bind("ps_found", a.apiFound))
	must(a.w.Bind("ps_scan", a.apiScan))
	must(a.w.Bind("ps_ignoreFound", a.apiIgnoreFound))
	must(a.w.Bind("ps_addFound", a.apiAddFound))
	must(a.w.Bind("ps_foundGame", a.apiFoundGame))
	must(a.w.Bind("ps_applyHooks", a.apiApplyHooks))
	must(a.w.Bind("ps_hide", func() { win.ShowWindow(a.hwnd, win.SW_HIDE) }))
	must(a.w.Bind("ps_quit", func() { a.ui(func() { a.quit(false) }) }))
}

func (a *App) apiReady() map[string]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	first := !a.ready
	a.ready = true
	p, r := a.pending, a.pendReq
	a.pending, a.pendReq = "", ""
	if first {
		go a.logf("Interface loaded")
	}
	return map[string]string{"launch": p, "reqId": r}
}

type SteamStatus struct {
	Found        bool     `json:"found"`
	Path         string   `json:"path"`
	Accounts     []string `json:"accounts"`
	AccountInUse string   `json:"accountInUse"`
	AccountError string   `json:"accountError,omitempty"`
}

type AppState struct {
	Version   string              `json:"version"`
	Config    *core.Config        `json:"config"`
	FirstRun  bool                `json:"firstRun"`
	LoadError string              `json:"loadError,omitempty"`
	Active    map[string]string   `json:"active"`
	Sessions  []Session           `json:"sessions"`
	Profiles  map[string][]string `json:"profiles"`
	Steam     SteamStatus         `json:"steam"`
	Autostart bool                `json:"autostart"`
	DataDir   string              `json:"dataDir"`
	Colors    []string            `json:"colors"`
}

func (a *App) apiState() AppState {
	a.mu.Lock()
	cfg := a.cfg.Clone()
	st := AppState{Version: Version, Config: cfg, FirstRun: a.firstRun, LoadError: a.loadErr, DataDir: DataDir(), Colors: core.PlayerColors}
	a.mu.Unlock()
	st.Active = a.store.LoadState()
	st.Sessions = a.sessionsSnapshot()
	st.Profiles = map[string][]string{}
	for _, g := range cfg.Games {
		list := []string{}
		for _, p := range cfg.Players {
			if a.eng.HasProfile(p.ID, g.ID) {
				list = append(list, p.ID)
			}
		}
		st.Profiles[g.ID] = list
	}
	st.Steam.Path = a.env.SteamPath
	st.Steam.Found = a.env.SteamPath != ""
	if st.Steam.Found {
		st.Steam.Accounts = core.SteamAccounts(a.env.SteamPath)
		if d, err := a.env.SteamUser(); err == nil {
			st.Steam.AccountInUse = filepath.Base(d)
		} else {
			st.Steam.AccountError = err.Error()
		}
	}
	_, st.Autostart = win.Autostart(Name)
	return st
}

func (a *App) saveConfig(cfg *core.Config) error {
	if err := a.store.SaveConfig(cfg); err != nil {
		return fmt.Errorf("couldn't save settings: %w", err)
	}
	a.mu.Lock()
	a.cfg = cfg
	a.firstRun = false
	a.mu.Unlock()
	return nil
}

func (a *App) activeSessionFor(gameID string) *Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessions[gameID]
}

// ---------------------------------------------------------------- players --

func (a *App) apiSavePlayers(players []core.Player) error {
	if err := core.ValidatePlayers(players); err != nil {
		return err
	}
	a.mu.Lock()
	cfg := a.cfg.Clone()
	keep := map[string]bool{}
	for _, p := range players {
		keep[p.ID] = true
	}
	var removed []string
	for _, p := range cfg.Players {
		if !keep[p.ID] {
			removed = append(removed, p.ID)
		}
	}
	for _, s := range a.sessions {
		for _, r := range removed {
			if s.PlayerID == r {
				a.mu.Unlock()
				return fmt.Errorf("%s is playing right now; remove them after the game closes.", cfg.PlayerName(r))
			}
		}
	}
	a.mu.Unlock()

	cfg.Players = players
	for i := range cfg.Games {
		for _, r := range removed {
			delete(cfg.Games[i].PlayerArgs, r)
		}
	}
	if err := a.saveConfig(cfg); err != nil {
		return err
	}
	state := a.store.LoadState()
	for _, r := range removed {
		a.archive(filepath.Join(a.store.ProfilesDir(), r), "player-"+r)
		for g, p := range state {
			if p == r {
				_ = a.store.SetActive(g, "")
			}
		}
		a.logf("Removed a player (%s)", r)
	}
	return nil
}

// archive moves data aside instead of deleting it, so a misclick is recoverable.
func (a *App) archive(src, label string) {
	if _, err := os.Stat(src); err != nil {
		return
	}
	dst := filepath.Join(a.store.Dir, "removed", label+"-"+time.Now().Format("20060102-150405"))
	_ = os.MkdirAll(filepath.Dir(dst), 0o755)
	if err := os.Rename(src, dst); err != nil {
		a.logf("ERROR archiving %s: %v", src, err)
	}
}

// ------------------------------------------------------------------ games --

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func (a *App) apiSaveGame(g core.Game) (core.Game, error) {
	isNew := g.ID == ""
	saved, err := a.apiSaveGameQuiet(g)
	if err == nil && isNew && saved.Launch.Type == "steam" {
		a.autoHooks()
	}
	return saved, err
}

func (a *App) apiSaveGameQuiet(g core.Game) (core.Game, error) {
	a.mu.Lock()
	cfg := a.cfg.Clone()
	a.mu.Unlock()
	isNew := g.ID == ""
	if isNew {
		slug := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(g.Name), "-"), "-")
		if len(slug) > 24 {
			slug = slug[:24]
		}
		if slug == "" {
			slug = "game"
		}
		g.ID = slug
		for i := 2; cfg.Game(g.ID) != nil; i++ {
			g.ID = fmt.Sprintf("%s-%d", slug, i)
		}
	} else if cfg.Game(g.ID) == nil {
		return g, errors.New("That game no longer exists.")
	}
	if err := core.ValidateGame(&g); err != nil {
		return g, err
	}
	for pid := range g.PlayerArgs {
		if cfg.Player(pid) == nil || strings.TrimSpace(g.PlayerArgs[pid]) == "" {
			delete(g.PlayerArgs, pid)
		}
	}
	if err := a.eng.CheckPaths(&g); err != nil {
		return g, err
	}
	if g.Launch.Type == "exe" {
		if _, err := a.env.ResolveExe(g.Launch.Exe); err != nil {
			return g, err
		}
	}
	if !isNew && a.activeSessionFor(g.ID) != nil {
		return g, fmt.Errorf("%s is in use right now; edit it after the game closes.", g.Name)
	}
	if isNew {
		cfg.Games = append(cfg.Games, g)
	} else {
		*cfg.Game(g.ID) = g
	}
	if err := a.saveConfig(cfg); err != nil {
		return g, err
	}
	a.logf("Saved game %s (%s)", g.Name, g.ID)
	return g, nil
}

func (a *App) apiDeleteGame(id string) error {
	if a.activeSessionFor(id) != nil {
		return errors.New("This game is in use right now; remove it after it closes.")
	}
	a.mu.Lock()
	cfg := a.cfg.Clone()
	a.mu.Unlock()
	out := cfg.Games[:0]
	var name string
	steamRemoved := false
	for _, g := range cfg.Games {
		if g.ID == id {
			name = g.Name
			if g.Launch.Type == "steam" {
				// put the game's own Steam launch options back
				if cfg.SteamRestores == nil {
					cfg.SteamRestores = map[string]string{}
				}
				cfg.SteamRestores[strconv.Itoa(g.Launch.AppID)] = g.SteamPrevOptions
				steamRemoved = true
			}
			continue
		}
		out = append(out, g)
	}
	cfg.Games = out
	if err := a.saveConfig(cfg); err != nil {
		return err
	}
	for _, p := range cfg.Players {
		a.archive(a.store.ProfileDir(p.ID, id), "game-"+id+"-"+p.ID)
	}
	_ = a.store.SetActive(id, "")
	a.logf("Removed game %s", name)
	if steamRemoved {
		a.autoHooksAlways()
	}
	return nil
}

// --------------------------------------------------------------- settings --

type SettingsPayload struct {
	Settings  core.Settings `json:"settings"`
	Autostart bool          `json:"autostart"`
}

func (a *App) apiSaveSettings(p SettingsPayload) error {
	s := p.Settings
	if s.BackupsToKeep < 1 || s.BackupsToKeep > 100 {
		return errors.New("Keep between 1 and 100 backups per game.")
	}
	if s.StartTimeoutSeconds < 30 || s.StartTimeoutSeconds > 1800 {
		return errors.New("The start wait must be between 30 and 1800 seconds.")
	}
	s.SteamAccountID = strings.TrimSpace(s.SteamAccountID)
	a.mu.Lock()
	cfg := a.cfg.Clone()
	a.mu.Unlock()
	cfg.Settings = s
	if err := a.saveConfig(cfg); err != nil {
		return err
	}
	if err := win.SetAutostart(Name, fmt.Sprintf(`"%s" --tray`, a.exePath), p.Autostart); err != nil {
		return fmt.Errorf("couldn't change the startup setting: %w", err)
	}
	if s.AutoSteamHooks {
		a.autoHooks()
	}
	return nil
}

// ------------------------------------------------------------ discovery ---

func (a *App) apiSteamGames() ([]core.SteamGame, error) {
	if a.env.SteamPath == "" {
		return nil, errors.New("Steam wasn't found on this PC.")
	}
	return core.InstalledSteamGames(a.env.SteamPath), nil
}

type Inspect struct {
	ProcessName string              `json:"processName"`
	Name        string              `json:"name"`
	Preset      *core.Preset        `json:"preset,omitempty"`
	Candidates  []core.ExeCandidate `json:"candidates"`
	Suggestions []core.Suggestion   `json:"suggestions"`
}

func (a *App) apiInspectSteam(appID int) (Inspect, error) {
	var game *core.SteamGame
	for _, g := range core.InstalledSteamGames(a.env.SteamPath) {
		if g.AppID == appID {
			g := g
			game = &g
			break
		}
	}
	if game == nil {
		return Inspect{}, errors.New("That Steam game isn't installed.")
	}
	out := Inspect{Name: game.Name}
	out.Candidates = core.ProcessCandidates(game.InstallDir)
	if p, ok := core.KnownSteamGames[appID]; ok {
		p := p
		out.Preset = &p
		out.ProcessName = p.ProcessName
	} else if len(out.Candidates) > 0 {
		out.ProcessName = out.Candidates[0].Name
	}
	out.Suggestions = a.env.SuggestPaths(game.Name, filepath.Base(game.InstallDir), appID)
	if out.Candidates == nil {
		out.Candidates = []core.ExeCandidate{}
	}
	if out.Suggestions == nil {
		out.Suggestions = []core.Suggestion{}
	}
	return out, nil
}

func (a *App) apiInspectExe(path string) (Inspect, error) {
	exe, err := a.env.ResolveExe(path)
	if err != nil {
		return Inspect{}, err
	}
	base := strings.TrimSuffix(filepath.Base(exe), filepath.Ext(exe))
	dir := filepath.Dir(exe)
	name := filepath.Base(dir)
	if strings.EqualFold(name, "bin") || strings.EqualFold(name, "win64") || strings.EqualFold(name, "x64") || strings.EqualFold(name, "binaries") {
		name = filepath.Base(filepath.Dir(dir))
	}
	out := Inspect{ProcessName: base, Name: name, Candidates: []core.ExeCandidate{}}
	out.Suggestions = a.env.SuggestPaths(name, base, 0)
	if out.Suggestions == nil {
		out.Suggestions = []core.Suggestion{}
	}
	return out, nil
}

type PathCheck struct {
	Resolved string `json:"resolved"`
	Exists   bool   `json:"exists"`
	IsDir    bool   `json:"isDir"`
	Size     int64  `json:"size"`
	Error    string `json:"error,omitempty"`
}

func (a *App) apiCheckPath(raw string) PathCheck {
	p, err := a.env.Resolve(raw)
	if err != nil {
		return PathCheck{Error: err.Error()}
	}
	out := PathCheck{Resolved: p}
	if st, err := os.Stat(p); err == nil {
		out.Exists, out.IsDir = true, st.IsDir()
		out.Size = core.Size(p)
	}
	return out
}

// apiBrowse opens a native picker after the current call returns; the result
// arrives as a "browse" event.
func (a *App) apiBrowse(reqID, kind, start string) {
	a.ui(func() {
		opt := win.BrowseOptions{Owner: a.hwnd}
		if start != "" {
			if p, err := a.env.Resolve(start); err == nil {
				for p != "" {
					if st, err := os.Stat(p); err == nil && st.IsDir() {
						break
					}
					parent := filepath.Dir(p)
					if parent == p {
						p = ""
						break
					}
					p = parent
				}
				opt.Start = p
			}
		}
		switch kind {
		case "exe":
			opt.Title = "Choose the game's .exe"
			opt.Filters = []win.FileFilter{{Name: "Programs (*.exe)", Spec: "*.exe"}}
		case "file":
			opt.Title = "Choose a settings or save file"
			opt.Filters = []win.FileFilter{{Name: "All files", Spec: "*.*"}}
		default:
			opt.Title = "Choose a settings or save folder"
			opt.Folder = true
		}
		path, err := win.Browse(opt)
		res := map[string]any{"reqId": reqID}
		switch {
		case errors.Is(err, win.ErrCancelled):
			res["cancelled"] = true
		case err != nil:
			res["error"] = err.Error()
		default:
			res["path"] = path
			res["tokenized"] = a.env.Tokenize(path)
		}
		a.emit("browse", res)
	})
}

// -------------------------------------------------------------- actions ---

func (a *App) apiPlay(r swapRequest) (Need, error) {
	r.Launch = true
	return a.startSwap(r)
}

func (a *App) apiLoad(r swapRequest) (Need, error) {
	r.Launch = false
	r.ReqID = ""
	return a.startSwap(r)
}

// apiSteamPick answers a Steam request: swap the files, then let the
// waiting wrapper start the game.
func (a *App) apiSteamPick(r swapRequest) (Need, error) {
	r.Launch = false
	a.mu.Lock()
	gid, ok := a.requests[r.ReqID]
	a.mu.Unlock()
	if !ok || gid != r.GameID {
		return Need{}, errors.New("Steam stopped waiting for this game. Press Play in Steam again.")
	}
	return a.startSwap(r)
}

var badFileChars = regexp.MustCompile(`[\\/:*?"<>|]+`)

func (a *App) apiShortcut(gameID string) (string, error) {
	a.mu.Lock()
	cfg := a.cfg.Clone()
	a.mu.Unlock()
	g := cfg.Game(gameID)
	if g == nil {
		return "", errors.New("That game no longer exists.")
	}
	desktop := win.Desktop()
	if desktop == "" {
		return "", errors.New("Couldn't find your Desktop folder.")
	}
	lnk := filepath.Join(desktop, strings.TrimSpace(badFileChars.ReplaceAllString(g.Name, " "))+" (PlaySwap).lnk")
	icon, iconIdx := a.exePath, 0
	switch g.Launch.Type {
	case "exe":
		if exe, err := a.env.ResolveExe(g.Launch.Exe); err == nil {
			icon = exe
		}
	case "steam":
		for _, sg := range core.InstalledSteamGames(a.env.SteamPath) {
			if sg.AppID == g.Launch.AppID {
				if exe := core.FindExe(sg.InstallDir, g.ProcessName); exe != "" {
					icon = exe
				}
				break
			}
		}
	}
	err := win.CreateShortcut(win.Shortcut{
		Path: lnk, Target: a.exePath, Args: "--launch " + g.ID, WorkDir: filepath.Dir(a.exePath),
		Icon: icon, IconIndex: iconIdx, Description: "Pick who's playing, then start " + g.Name,
	})
	if err != nil {
		return "", fmt.Errorf("couldn't create the shortcut: %w", err)
	}
	found := false
	for _, s := range cfg.Shortcuts {
		if strings.EqualFold(s, lnk) {
			found = true
		}
	}
	if !found {
		cfg.Shortcuts = append(cfg.Shortcuts, lnk)
		_ = a.saveConfig(cfg)
	}
	a.logf("Created shortcut %s", lnk)
	return lnk, nil
}

func (a *App) apiOpen(kind, arg string) error {
	var target string
	switch kind {
	case "data":
		target = DataDir()
	case "backups":
		target = filepath.Join(a.store.BackupsDir(), filepath.Base(arg))
	case "profile":
		target = filepath.Join(a.store.ProfilesDir(), filepath.Base(arg))
	case "path":
		p, err := a.env.Resolve(arg)
		if err != nil {
			return err
		}
		for {
			if _, err := os.Stat(p); err == nil {
				break
			}
			parent := filepath.Dir(p)
			if parent == p {
				return errors.New("That folder doesn't exist yet. Start the game once so it creates it.")
			}
			p = parent
		}
		target = p
	case "pcgw":
		target = "https://www.pcgamingwiki.com/w/index.php?search=" + url.QueryEscape(arg)
		return win.ShellOpen(target)
	case "log":
		target = a.store.LogPath()
	default:
		return errors.New("unknown location")
	}
	if _, err := os.Stat(target); err != nil {
		if kind == "log" {
			return errors.New("Nothing has been logged yet.")
		}
		_ = os.MkdirAll(target, 0o755)
	}
	return win.ShellOpen(target)
}

func (a *App) apiLog() string {
	b, err := os.ReadFile(a.store.LogPath())
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\r\n"), "\n")
	if len(lines) > 300 {
		lines = lines[len(lines)-300:]
	}
	return strings.Join(lines, "\n")
}
