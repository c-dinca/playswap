//go:build windows

package app

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"playswap/internal/core"
	"playswap/internal/win"
)

// Session tracks one game being played.
type Session struct {
	GameID   string    `json:"gameId"`
	PlayerID string    `json:"playerId"`
	Phase    string    `json:"phase"` // loading, launching, playing, saving
	Outside  bool      `json:"outside"`
	Since    time.Time `json:"since"`
	deadline time.Time
	missed   int
	viaPlay  bool // started from PlaySwap's own Play button
}

func (a *App) sessionsSnapshot() []Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Session, 0, len(a.sessions))
	for _, s := range a.sessions {
		out = append(out, *s)
	}
	return out
}

func (a *App) emitSessions() {
	a.emit("sessions", map[string]any{"sessions": a.sessionsSnapshot(), "active": a.store.LoadState()})
	a.updateTrayTip()
}

func (a *App) updateTrayTip() {
	if a.tray == nil {
		return
	}
	a.mu.Lock()
	tip := "PlaySwap"
	for _, s := range a.sessions {
		if s.Phase == "playing" {
			tip = fmt.Sprintf("PlaySwap: %s is playing %s", a.cfg.PlayerName(s.PlayerID), a.gameName(s.GameID))
			break
		}
	}
	a.mu.Unlock()
	a.ui(func() { a.tray.SetTip(tip) })
}

func (a *App) gameName(id string) string {
	if g := a.cfg.Game(id); g != nil {
		return g.Name
	}
	return id
}

func procName(g *core.Game) string {
	return strings.ToLower(strings.TrimSuffix(g.ProcessName, ".exe"))
}

func isRunning(g *core.Game) bool {
	procs, err := win.ProcessNames()
	return err == nil && procs[procName(g)]
}

// Need is returned to the UI when a choice is still required.
type Need struct {
	Need string `json:"need,omitempty"` // "owner" | "newMode"
}

type swapRequest struct {
	GameID     string `json:"gameId"`
	PlayerID   string `json:"playerId"`
	FirstOwner string `json:"firstOwner"`
	NewProfile string `json:"newProfile"`
	Launch     bool   `json:"launch"`
	ReqID      string `json:"reqId"` // Steam request waiting for this swap
}

// startSwap validates a play/load request and runs it in the background.
func (a *App) startSwap(req swapRequest) (Need, error) {
	a.mu.Lock()
	cfg := a.cfg.Clone()
	g := cfg.Game(req.GameID)
	if g == nil {
		a.mu.Unlock()
		return Need{}, errors.New("That game no longer exists.")
	}
	if cfg.Player(req.PlayerID) == nil {
		a.mu.Unlock()
		return Need{}, errors.New("That player no longer exists.")
	}
	if s := a.sessions[g.ID]; s != nil {
		a.mu.Unlock()
		if s.Phase == "playing" {
			return Need{}, fmt.Errorf("%s is already being played by %s.", g.Name, cfg.PlayerName(s.PlayerID))
		}
		return Need{}, fmt.Errorf("%s is busy right now; try again in a moment.", g.Name)
	}
	a.mu.Unlock()

	if isRunning(g) {
		return Need{}, fmt.Errorf("%s is already running. Close it first, then pick who's playing.", g.Name)
	}
	if err := a.eng.CheckPaths(g); err != nil {
		return Need{}, err
	}
	active := a.store.LoadState()[g.ID]
	if active != "" && cfg.Player(active) == nil {
		active = ""
	}
	if active == "" && req.FirstOwner == "" {
		return Need{Need: "owner"}, nil
	}
	if active != req.PlayerID && req.FirstOwner != req.PlayerID && !a.eng.HasProfile(req.PlayerID, g.ID) && req.NewProfile == "" {
		return Need{Need: "newMode"}, nil
	}

	a.mu.Lock()
	if a.sessions[g.ID] != nil {
		a.mu.Unlock()
		return Need{}, fmt.Errorf("%s is busy right now; try again in a moment.", g.Name)
	}
	a.sessions[g.ID] = &Session{GameID: g.ID, PlayerID: req.PlayerID, Phase: "loading", Since: time.Now(), viaPlay: req.Launch}
	a.mu.Unlock()
	a.emitSessions()

	go a.runSwap(cfg, g, req)
	return Need{}, nil
}

func (a *App) runSwap(cfg *core.Config, g *core.Game, req swapRequest) {
	who := cfg.PlayerName(req.PlayerID)
	fail := func(err error) {
		a.mu.Lock()
		delete(a.sessions, g.ID)
		a.mu.Unlock()
		a.logf("ERROR %s / %s: %v", g.Name, who, err)
		a.emit("toast", map[string]any{"kind": "error", "text": err.Error()})
		a.emitSessions()
	}
	err := a.eng.Activate(cfg, g.ID, req.PlayerID, core.ActivateOptions{
		FirstOwner: req.FirstOwner,
		NewProfile: req.NewProfile,
		Keep:       cfg.Settings.BackupsToKeep,
	})
	if err != nil {
		a.answerRequest(req.ReqID, "cancel", "")
		fail(err)
		return
	}
	if req.ReqID != "" {
		// Steam's wrapper starts the game; the watcher follows it from here.
		a.mu.Lock()
		if s := a.sessions[g.ID]; s != nil {
			s.Phase = "launching"
			s.deadline = time.Now().Add(time.Duration(cfg.Settings.StartTimeoutSeconds) * time.Second)
		}
		hide := cfg.Settings.HideWhilePlaying
		a.mu.Unlock()
		a.answerRequest(req.ReqID, "run", "")
		a.logf("Steam is starting %s for %s", g.Name, who)
		a.emitSessions()
		if hide {
			a.ui(func() { win.ShowWindow(a.hwnd, win.SW_HIDE) })
		}
		return
	}
	if !req.Launch {
		a.mu.Lock()
		delete(a.sessions, g.ID)
		a.mu.Unlock()
		a.emit("toast", map[string]any{"kind": "ok", "text": fmt.Sprintf("%s's files are now loaded for %s.", who, g.Name)})
		a.emitSessions()
		return
	}
	if err := a.launchGame(cfg, g, req.PlayerID); err != nil {
		// The swap succeeded; files stay loaded for this player.
		fail(fmt.Errorf("%s's files are loaded, but %s didn't launch: %v", who, g.Name, err))
		return
	}
	a.logf("Launched %s for %s", g.Name, who)
	a.mu.Lock()
	if s := a.sessions[g.ID]; s != nil {
		s.Phase = "launching"
		s.deadline = time.Now().Add(time.Duration(cfg.Settings.StartTimeoutSeconds) * time.Second)
	}
	hide := cfg.Settings.HideWhilePlaying
	a.mu.Unlock()
	a.emitSessions()
	if hide {
		a.ui(func() { win.ShowWindow(a.hwnd, win.SW_HIDE) })
	}
}

func (a *App) launchGame(cfg *core.Config, g *core.Game, playerID string) error {
	args := strings.TrimSpace(g.Launch.Args + " " + g.PlayerArgs[playerID])
	switch g.Launch.Type {
	case "steam":
		if a.env.SteamPath == "" {
			return errors.New("Steam wasn't found on this PC")
		}
		line := fmt.Sprintf("-applaunch %d", g.Launch.AppID)
		if args != "" {
			line += " " + args
		}
		return win.StartDetached(a.env.SteamPath+`\steam.exe`, line, a.env.SteamPath)
	case "exe":
		exe, err := a.env.ResolveExe(g.Launch.Exe)
		if err != nil {
			return err
		}
		return win.StartDetached(exe, args, dirOf(exe))
	}
	return fmt.Errorf("unknown launch type %q", g.Launch.Type)
}

// watch runs forever: it follows every managed game's process, finishes
// sessions when games close, and picks up games launched outside PlaySwap.
func (a *App) watch() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	n := 0
	for range t.C {
		a.tick()
		n++
		if n%15 == 0 { // every ~30 s: finish Steam set-up once Steam is closed
			a.mu.Lock()
			pending := a.hooksPending
			a.mu.Unlock()
			if pending && !steamRunning() {
				a.autoHooksAlways()
			}
		}
	}
}

func (a *App) tick() {
	procs, err := win.ProcessNames()
	if err != nil {
		return
	}
	state := a.store.LoadState()
	changed := false
	type finish struct {
		cfg  *core.Config
		game *core.Game
		pid  string
	}
	var finishes []finish
	var notes [][2]string

	a.mu.Lock()
	cfg := a.cfg
	for i := range cfg.Games {
		g := &cfg.Games[i]
		running := procs[procName(g)]
		s := a.sessions[g.ID]
		switch {
		case s == nil && running:
			if !cfg.Settings.WatchOutsideLaunches {
				continue
			}
			owner := state[g.ID]
			if owner == "" || cfg.Player(owner) == nil {
				continue
			}
			a.sessions[g.ID] = &Session{GameID: g.ID, PlayerID: owner, Phase: "playing", Outside: true, Since: time.Now()}
			changed = true
			a.logf("%s started outside PlaySwap; %s's files are loaded", g.Name, cfg.PlayerName(owner))
			notes = append(notes, [2]string{g.Name + " started",
				fmt.Sprintf("It's using %s's settings and saves. Progress will be saved to %s when the game closes.", cfg.PlayerName(owner), cfg.PlayerName(owner))})
		case s != nil && s.Phase == "launching":
			if running {
				s.Phase = "playing"
				s.Since = time.Now()
				changed = true
			} else if time.Now().After(s.deadline) {
				delete(a.sessions, g.ID)
				changed = true
				notes = append(notes, [2]string{g.Name + " didn't start",
					fmt.Sprintf("%s's files stay loaded. If it starts later, PlaySwap will still save it.", cfg.PlayerName(s.PlayerID))})
			}
		case s != nil && s.Phase == "playing":
			if running {
				s.missed = 0
				continue
			}
			s.missed++
			if s.missed < 2 { // ignore quick restarts (launcher -> game)
				continue
			}
			s.Phase = "saving"
			changed = true
			finishes = append(finishes, finish{cfg.Clone(), cfg.Game(g.ID), s.PlayerID})
		}
	}
	a.mu.Unlock()

	if changed {
		a.emitSessions()
	}
	for _, n := range notes {
		a.notify(n[0], n[1])
	}
	for _, f := range finishes {
		go a.finishSession(f.cfg, f.game, f.pid)
	}
}

func (a *App) finishSession(cfg *core.Config, g *core.Game, playerID string) {
	time.Sleep(3 * time.Second) // let the game and Steam finish writing
	err := a.eng.FinishSession(cfg, g.ID, playerID)
	a.mu.Lock()
	delete(a.sessions, g.ID)
	a.mu.Unlock()
	who := cfg.PlayerName(playerID)
	if err != nil {
		a.logf("ERROR saving %s for %s: %v", g.Name, who, err)
		a.notify("Couldn't save "+g.Name, err.Error())
		a.emit("toast", map[string]any{"kind": "error", "text": err.Error()})
	} else {
		a.notify("Saved "+who+"'s "+g.Name, "Settings and saves are stored in "+who+"'s profile.")
	}
	a.emitSessions()
}
