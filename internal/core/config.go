// Package core holds PlaySwap's platform-neutral logic: configuration,
// path resolution, profile storage and file swapping.
package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const ConfigVersion = 1

type Player struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type PathEntry struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

type Launch struct {
	// Type is "steam" or "exe".
	Type  string `json:"type"`
	AppID int    `json:"appId,omitempty"`
	Exe   string `json:"exe,omitempty"`
	Args  string `json:"args,omitempty"`
}

type Game struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Launch      Launch            `json:"launch"`
	ProcessName string            `json:"processName"`
	PlayerArgs  map[string]string `json:"playerArgs,omitempty"`
	Paths       []PathEntry       `json:"paths"`
	// SteamPrevOptions are the Steam launch options the game had before
	// PlaySwap added itself; they're put back when PlaySwap lets go.
	SteamPrevOptions string `json:"steamPrevOptions,omitempty"`
}

type Settings struct {
	StartMinimized       bool   `json:"startMinimized"`
	CloseToTray          bool   `json:"closeToTray"`
	HideWhilePlaying     bool   `json:"hideWhilePlaying"`
	WatchOutsideLaunches bool   `json:"watchOutsideLaunches"`
	Notifications        bool   `json:"notifications"`
	BackupsToKeep        int    `json:"backupsToKeep"`
	StartTimeoutSeconds  int    `json:"startTimeoutSeconds"`
	SteamAccountID       string `json:"steamAccountId"`
	AutoSteamHooks       bool   `json:"autoSteamHooks"`
}

type Config struct {
	Version   int      `json:"version"`
	Players   []Player `json:"players"`
	Games     []Game   `json:"games"`
	Settings  Settings `json:"settings"`
	Shortcuts []string `json:"shortcuts,omitempty"` // desktop shortcuts PlaySwap created, removed on uninstall
	// SteamRestores: launch options to put back for Steam games removed from
	// PlaySwap (app id -> original options), applied when Steam is closed.
	SteamRestores map[string]string `json:"steamRestores,omitempty"`
	// IgnoredFound: scan results the user chose to hide.
	IgnoredFound []string `json:"ignoredFound,omitempty"`
}

// PlayerColors is the palette offered in the UI. The first two are the defaults.
var PlayerColors = []string{"#4F8CFF", "#FF5D8F", "#2EC4A0", "#FFB020", "#A77BFF", "#FF7A45"}

func DefaultConfig() *Config {
	return &Config{
		Version: ConfigVersion,
		Players: []Player{
			{ID: "p1", Name: "Player 1", Color: PlayerColors[0]},
			{ID: "p2", Name: "Player 2", Color: PlayerColors[1]},
		},
		Games: []Game{},
		Settings: Settings{
			StartMinimized:       true,
			CloseToTray:          true,
			HideWhilePlaying:     true,
			WatchOutsideLaunches: true,
			Notifications:        true,
			BackupsToKeep:        10,
			StartTimeoutSeconds:  240,
			AutoSteamHooks:       true,
		},
	}
}

func (c *Config) normalize() {
	if c.Version == 0 {
		c.Version = ConfigVersion
	}
	if c.Players == nil {
		c.Players = []Player{}
	}
	if c.Games == nil {
		c.Games = []Game{}
	}
	if c.Settings.BackupsToKeep <= 0 {
		c.Settings.BackupsToKeep = 10
	}
	if c.Settings.StartTimeoutSeconds <= 0 {
		c.Settings.StartTimeoutSeconds = 240
	}
	for i := range c.Games {
		if c.Games[i].Paths == nil {
			c.Games[i].Paths = []PathEntry{}
		}
	}
}

func (c *Config) Game(id string) *Game {
	for i := range c.Games {
		if c.Games[i].ID == id {
			return &c.Games[i]
		}
	}
	return nil
}

func (c *Config) Player(id string) *Player {
	for i := range c.Players {
		if c.Players[i].ID == id {
			return &c.Players[i]
		}
	}
	return nil
}

func (c *Config) PlayerName(id string) string {
	if p := c.Player(id); p != nil {
		return p.Name
	}
	return id
}

func (c *Config) Clone() *Config {
	b, _ := json.Marshal(c)
	var out Config
	_ = json.Unmarshal(b, &out)
	return &out
}

func NewID(prefix string) string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,40}$`)
var colorRe = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// ValidateGame checks a game definition. It does not touch the filesystem.
func ValidateGame(g *Game) error {
	g.Name = strings.TrimSpace(g.Name)
	g.ProcessName = strings.TrimSpace(g.ProcessName)
	g.ProcessName = strings.TrimSuffix(strings.TrimSuffix(g.ProcessName, ".exe"), ".EXE")
	if g.Name == "" {
		return errors.New("Give the game a name.")
	}
	if !idRe.MatchString(g.ID) {
		return fmt.Errorf("invalid game id %q", g.ID)
	}
	switch g.Launch.Type {
	case "steam":
		if g.Launch.AppID <= 0 {
			return errors.New("Pick the Steam game to launch.")
		}
	case "exe":
		if strings.TrimSpace(g.Launch.Exe) == "" {
			return errors.New("Choose the game's .exe file.")
		}
	default:
		return errors.New("Choose how the game is launched.")
	}
	if g.ProcessName == "" || strings.ContainsAny(g.ProcessName, `\/:*?"<>|`) {
		return errors.New("Set the game's process name (the .exe that runs while you play).")
	}
	if len(g.Paths) == 0 {
		return errors.New("Add at least one folder or file that holds the game's settings or saves.")
	}
	seen := map[string]bool{}
	for i := range g.Paths {
		p := &g.Paths[i]
		p.Path = strings.TrimSpace(p.Path)
		if p.ID == "" {
			p.ID = NewID("d")
		}
		if !idRe.MatchString(p.ID) {
			return fmt.Errorf("invalid path id %q", p.ID)
		}
		if p.Path == "" {
			return errors.New("One of the save locations is empty.")
		}
		key := strings.ToLower(p.Path)
		if seen[key] {
			return fmt.Errorf("%s is listed twice.", p.Path)
		}
		seen[key] = true
	}
	if g.PlayerArgs == nil {
		g.PlayerArgs = map[string]string{}
	}
	return nil
}

func ValidatePlayers(ps []Player) error {
	if len(ps) < 1 {
		return errors.New("Keep at least one player.")
	}
	if len(ps) > 8 {
		return errors.New("PlaySwap supports up to 8 players.")
	}
	names := map[string]bool{}
	ids := map[string]bool{}
	for i := range ps {
		p := &ps[i]
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" {
			return errors.New("Every player needs a name.")
		}
		if len([]rune(p.Name)) > 24 {
			return fmt.Errorf("%q is too long; keep names under 24 characters.", p.Name)
		}
		if names[strings.ToLower(p.Name)] {
			return fmt.Errorf("Two players are called %s.", p.Name)
		}
		names[strings.ToLower(p.Name)] = true
		if p.ID == "" {
			p.ID = NewID("p")
		}
		if !idRe.MatchString(p.ID) || ids[p.ID] {
			return fmt.Errorf("invalid player id %q", p.ID)
		}
		ids[p.ID] = true
		if !colorRe.MatchString(p.Color) {
			p.Color = PlayerColors[i%len(PlayerColors)]
		}
	}
	return nil
}

// ---------------------------------------------------------------- storage ---

// Store owns the on-disk layout:
//
//	<dir>/config.json
//	<dir>/state.json            gameID -> playerID whose files are live
//	<dir>/profiles/<player>/<game>/<pathID>
//	<dir>/backups/<game>/<timestamp>/<pathID>
//	<dir>/playswap.log
type Store struct {
	Dir string
	mu  sync.Mutex
}

func (s *Store) ConfigPath() string  { return filepath.Join(s.Dir, "config.json") }
func (s *Store) StatePath() string   { return filepath.Join(s.Dir, "state.json") }
func (s *Store) ProfilesDir() string { return filepath.Join(s.Dir, "profiles") }
func (s *Store) BackupsDir() string  { return filepath.Join(s.Dir, "backups") }
func (s *Store) LogPath() string     { return filepath.Join(s.Dir, "playswap.log") }
func (s *Store) ProfileDir(playerID, gameID string) string {
	return filepath.Join(s.ProfilesDir(), playerID, gameID)
}

// LoadConfig returns the saved config, or defaults (and firstRun=true) if none exists.
func (s *Store) LoadConfig() (cfg *Config, firstRun bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.ConfigPath())
	if errors.Is(err, os.ErrNotExist) {
		return DefaultConfig(), true, nil
	}
	if err != nil {
		return nil, false, err
	}
	cfg = DefaultConfig()
	cfg.Games = nil
	cfg.Players = nil
	if err := json.Unmarshal(b, cfg); err != nil {
		// Keep the broken file for the user, start from defaults.
		_ = os.Rename(s.ConfigPath(), s.ConfigPath()+".broken")
		return DefaultConfig(), true, fmt.Errorf("config.json was unreadable and was renamed to config.json.broken: %w", err)
	}
	cfg.normalize()
	return cfg, false, nil
}

func (s *Store) SaveConfig(cfg *Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg.normalize()
	return writeJSONAtomic(s.ConfigPath(), cfg)
}

func (s *Store) LoadState() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := map[string]string{}
	b, err := os.ReadFile(s.StatePath())
	if err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func (s *Store) SetActive(gameID, playerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := map[string]string{}
	if b, err := os.ReadFile(s.StatePath()); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	if playerID == "" {
		delete(st, gameID)
	} else {
		st[gameID] = playerID
	}
	return writeJSONAtomic(s.StatePath(), st)
}

func writeJSONAtomic(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
