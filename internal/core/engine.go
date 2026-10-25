package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var (
	// ErrNeedOwner: PlaySwap has never managed this game, so it must be told
	// whose files are currently on the PC before it can swap.
	ErrNeedOwner = errors.New("need-owner")
	// ErrNeedNewMode: the chosen player has no profile yet for this game; the
	// caller must say whether to start from a copy or fresh.
	ErrNeedNewMode = errors.New("need-new-mode")
)

const (
	OwnerNobody = "none"
	NewCopy     = "copy"
	NewFresh    = "fresh"
)

const initMarker = ".initialized"

type Engine struct {
	Store *Store
	Env   *Env
	Log   func(format string, args ...any)
	Now   func() time.Time
	// Name turns a player ID into a display name for the log.
	Name func(playerID string) string
}

func (e *Engine) name(id string) string {
	if e.Name != nil {
		return e.Name(id)
	}
	return id
}

func (e *Engine) logf(format string, args ...any) {
	if e.Log != nil {
		e.Log(format, args...)
	}
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

type resolved struct {
	entry PathEntry
	live  string
}

// resolveAll resolves every path of a game up front so a bad path aborts
// before anything on disk is touched.
func (e *Engine) resolveAll(g *Game) ([]resolved, error) {
	out := make([]resolved, 0, len(g.Paths))
	for _, p := range g.Paths {
		live, err := e.Env.Resolve(p.Path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.Path, err)
		}
		out = append(out, resolved{p, live})
	}
	// Two entries must not overlap, or mirroring one would wipe the other.
	for i := range out {
		for j := range out {
			if i != j && isWithin(out[i].live, out[j].live) {
				return nil, fmt.Errorf("%s is inside %s; keep only the outer one", out[i].entry.Path, out[j].entry.Path)
			}
		}
	}
	return out, nil
}

// CheckPaths validates a game's paths without touching anything.
func (e *Engine) CheckPaths(g *Game) error {
	_, err := e.resolveAll(g)
	return err
}

func (e *Engine) HasProfile(playerID, gameID string) bool {
	return exists(filepath.Join(e.Store.ProfileDir(playerID, gameID), initMarker))
}

// SaveLiveTo copies the game's live files into a player's profile.
func (e *Engine) SaveLiveTo(g *Game, playerID string) error {
	rs, err := e.resolveAll(g)
	if err != nil {
		return err
	}
	dir := e.Store.ProfileDir(playerID, g.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, r := range rs {
		if err := Mirror(r.live, filepath.Join(dir, r.entry.ID)); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, initMarker), []byte(e.now().Format(time.RFC3339)), 0o644); err != nil {
		return err
	}
	e.logf("Saved %s's files for %s", e.name(playerID), g.Name)
	return nil
}

// restore copies a player's profile into the live locations.
func (e *Engine) restore(g *Game, playerID string, rs []resolved) error {
	dir := e.Store.ProfileDir(playerID, g.ID)
	for _, r := range rs {
		if err := Mirror(filepath.Join(dir, r.entry.ID), r.live); err != nil {
			return err
		}
	}
	e.logf("Loaded %s's files for %s", e.name(playerID), g.Name)
	return nil
}

// Backup copies the current live files to backups/<game>/<timestamp> and
// prunes old backups.
func (e *Engine) Backup(g *Game, keep int) (string, error) {
	rs, err := e.resolveAll(g)
	if err != nil {
		return "", err
	}
	root := filepath.Join(e.Store.BackupsDir(), g.ID)
	stamp := e.now().Format("20060102-150405")
	dir := filepath.Join(root, stamp)
	for n := 2; exists(dir); n++ {
		dir = filepath.Join(root, fmt.Sprintf("%s-%d", stamp, n))
	}
	any := false
	for _, r := range rs {
		if exists(r.live) {
			if err := Mirror(r.live, filepath.Join(dir, r.entry.ID)); err != nil {
				return "", err
			}
			any = true
		}
	}
	if any {
		st := e.Store.LoadState()
		_ = os.WriteFile(filepath.Join(dir, "owner.txt"), []byte(st[g.ID]), 0o644)
	}
	e.pruneBackups(root, keep)
	if !any {
		return "", nil
	}
	return dir, nil
}

func (e *Engine) pruneBackups(root string, keep int) {
	if keep <= 0 {
		keep = 10
	}
	ents, err := os.ReadDir(root)
	if err != nil {
		return
	}
	var dirs []string
	for _, d := range ents {
		if d.IsDir() {
			dirs = append(dirs, d.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for i, d := range dirs {
		if i >= keep {
			_ = removeAll(filepath.Join(root, d))
		}
	}
}

type ActivateOptions struct {
	// FirstOwner answers ErrNeedOwner: a player ID, or OwnerNobody.
	FirstOwner string
	// NewProfile answers ErrNeedNewMode: NewCopy or NewFresh.
	NewProfile string
	Keep       int
}

// Activate puts playerID's files in place for the game. The files currently
// in place are first saved back to whoever they belong to.
func (e *Engine) Activate(cfg *Config, gameID, playerID string, opt ActivateOptions) error {
	g := cfg.Game(gameID)
	if g == nil {
		return fmt.Errorf("unknown game %q", gameID)
	}
	if cfg.Player(playerID) == nil {
		return fmt.Errorf("unknown player %q", playerID)
	}
	rs, err := e.resolveAll(g)
	if err != nil {
		return err
	}

	active := e.Store.LoadState()[gameID]
	if active != "" && cfg.Player(active) == nil {
		active = "" // that player was deleted
	}
	if active == "" {
		switch {
		case opt.FirstOwner == "":
			return ErrNeedOwner
		case opt.FirstOwner == OwnerNobody:
			// Files on disk belong to nobody we know; they get backed up below.
		case cfg.Player(opt.FirstOwner) != nil:
			if err := e.SaveLiveTo(g, opt.FirstOwner); err != nil {
				return err
			}
			if err := e.Store.SetActive(gameID, opt.FirstOwner); err != nil {
				return err
			}
			active = opt.FirstOwner
		default:
			return fmt.Errorf("unknown player %q", opt.FirstOwner)
		}
	} else {
		if err := e.SaveLiveTo(g, active); err != nil {
			return err
		}
	}

	if active == playerID {
		return nil
	}

	if e.HasProfile(playerID, gameID) {
		if _, err := e.Backup(g, opt.Keep); err != nil {
			return err
		}
		if err := e.restore(g, playerID, rs); err != nil {
			return err
		}
	} else {
		switch opt.NewProfile {
		case NewCopy:
			if err := e.SaveLiveTo(g, playerID); err != nil {
				return err
			}
		case NewFresh:
			if _, err := e.Backup(g, opt.Keep); err != nil {
				return err
			}
			for _, r := range rs {
				if err := removeAll(r.live); err != nil {
					return fmt.Errorf("couldn't clear %s: %w", r.live, err)
				}
			}
			// Mark the profile as existing (empty) so the next swap restores
			// "nothing" for this player instead of asking again.
			if err := e.SaveLiveTo(g, playerID); err != nil {
				return err
			}
		default:
			return ErrNeedNewMode
		}
	}
	return e.Store.SetActive(gameID, playerID)
}

// FinishSession saves the live files back to the player who just played.
func (e *Engine) FinishSession(cfg *Config, gameID, playerID string) error {
	g := cfg.Game(gameID)
	if g == nil {
		return nil // game was removed meanwhile
	}
	if active := e.Store.LoadState()[gameID]; active != playerID {
		return fmt.Errorf("%s's files are no longer the ones loaded for %s; not saving", cfg.PlayerName(playerID), g.Name)
	}
	return e.SaveLiveTo(g, playerID)
}

// DeleteGameData removes all profiles, backups and state for a game.
func (e *Engine) DeleteGameData(cfg *Config, gameID string) {
	for _, p := range cfg.Players {
		_ = removeAll(e.Store.ProfileDir(p.ID, gameID))
	}
	_ = removeAll(filepath.Join(e.Store.BackupsDir(), gameID))
	_ = e.Store.SetActive(gameID, "")
}

// DeletePlayerData removes a player's profiles.
func (e *Engine) DeletePlayerData(playerID string) {
	if strings.TrimSpace(playerID) == "" {
		return
	}
	_ = removeAll(filepath.Join(e.Store.ProfilesDir(), playerID))
}

type BackupInfo struct {
	Dir   string `json:"dir"`
	When  string `json:"when"`
	Owner string `json:"owner"`
}

func (e *Engine) Backups(gameID string) []BackupInfo {
	root := filepath.Join(e.Store.BackupsDir(), gameID)
	ents, _ := os.ReadDir(root)
	var out []BackupInfo
	for i := len(ents) - 1; i >= 0; i-- {
		d := ents[i]
		if !d.IsDir() {
			continue
		}
		bi := BackupInfo{Dir: filepath.Join(root, d.Name())}
		if t, err := time.ParseInLocation("20060102-150405", d.Name()[:min(15, len(d.Name()))], time.Local); err == nil {
			bi.When = t.Format(time.RFC3339)
		}
		if b, err := os.ReadFile(filepath.Join(bi.Dir, "owner.txt")); err == nil {
			bi.Owner = strings.TrimSpace(string(b))
		}
		out = append(out, bi)
	}
	return out
}
