package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Env describes where things live on this machine. The Windows layer fills it
// in; tests fill it with temp folders.
type Env struct {
	Getenv     func(string) string
	Home       string
	Documents  string
	SavedGames string
	LocalLow   string
	SteamPath  string
	// SteamUser returns Steam\userdata\<accountID> for the account in use.
	SteamUser func() (string, error)
	// DataDir is PlaySwap's own data folder; game paths may never point into it.
	DataDir string
	// ExtraProtected are more folders that may never be (or contain) a game path,
	// e.g. Program Files, Windows, Desktop.
	ExtraProtected []string
}

var envVarRe = regexp.MustCompile(`%([A-Za-z0-9_()]+)%`)

// Tokens the user can put in a path, besides %ENVVARS%.
const (
	TokSteam      = "{STEAM}"
	TokSteamUser  = "{STEAMUSER}"
	TokDocuments  = "{DOCUMENTS}"
	TokSavedGames = "{SAVEDGAMES}"
	TokLocalLow   = "{LOCALLOW}"
)

// Resolve turns a stored path (with tokens/env vars) into an absolute path and
// checks it is safe to mirror into.
func (e *Env) Resolve(raw string) (string, error) {
	p, err := e.expand(raw)
	if err != nil {
		return "", err
	}
	if err := e.CheckSafe(p); err != nil {
		return "", err
	}
	return p, nil
}

// ResolveExe expands a launch path. Unlike Resolve it allows any location
// (games live in Program Files), but the file must exist.
func (e *Env) ResolveExe(raw string) (string, error) {
	p, err := e.expand(raw)
	if err != nil {
		return "", err
	}
	if st, err := os.Stat(p); err != nil || st.IsDir() {
		return "", fmt.Errorf("%s doesn't exist", p)
	}
	return p, nil
}

func (e *Env) expand(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", errors.New("empty path")
	}
	type tok struct {
		name string
		val  func() (string, error)
	}
	fixed := func(v, what string) func() (string, error) {
		return func() (string, error) {
			if v == "" {
				return "", fmt.Errorf("%s folder not found on this PC", what)
			}
			return v, nil
		}
	}
	toks := []tok{
		{TokSteamUser, func() (string, error) {
			if e.SteamUser == nil {
				return "", errors.New("Steam user folder not found")
			}
			return e.SteamUser()
		}},
		{TokSteam, fixed(e.SteamPath, "Steam")},
		{TokDocuments, fixed(e.Documents, "Documents")},
		{TokSavedGames, fixed(e.SavedGames, "Saved Games")},
		{TokLocalLow, fixed(e.LocalLow, "AppData\\LocalLow")},
	}
	for _, t := range toks {
		if strings.Contains(strings.ToUpper(p), t.name) {
			v, err := t.val()
			if err != nil {
				return "", err
			}
			p = replaceFold(p, t.name, v)
		}
	}
	var missing string
	p = envVarRe.ReplaceAllStringFunc(p, func(m string) string {
		name := m[1 : len(m)-1]
		v := ""
		if e.Getenv != nil {
			v = e.Getenv(name)
		}
		if v == "" && missing == "" {
			missing = name
		}
		return v
	})
	if missing != "" {
		return "", fmt.Errorf("%%%s%% is not set on this PC", missing)
	}
	if strings.ContainsAny(p, "{}") {
		return "", fmt.Errorf("unknown {TOKEN} in %s", raw)
	}
	p = filepath.Clean(p)
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%s is not a full path", raw)
	}
	return p, nil
}

func replaceFold(s, old, new string) string {
	i := strings.Index(strings.ToUpper(s), strings.ToUpper(old))
	if i < 0 {
		return s
	}
	return s[:i] + new + s[i+len(old):]
}

func (e *Env) protected() []string {
	var out []string
	add := func(p string) {
		if p != "" {
			out = append(out, filepath.Clean(p))
		}
	}
	add(e.Home)
	add(e.Documents)
	add(e.SavedGames)
	add(e.LocalLow)
	add(e.SteamPath)
	if e.SteamPath != "" {
		add(filepath.Join(e.SteamPath, "userdata"))
		add(filepath.Join(e.SteamPath, "steamapps"))
		add(filepath.Join(e.SteamPath, "steamapps", "common"))
	}
	if e.SteamUser != nil {
		if su, err := e.SteamUser(); err == nil {
			add(su)
		}
	}
	if e.Getenv != nil {
		for _, v := range []string{"APPDATA", "LOCALAPPDATA", "USERPROFILE", "PROGRAMDATA", "PUBLIC", "TEMP", "TMP"} {
			add(e.Getenv(v))
		}
		if d := e.Getenv("USERPROFILE"); d != "" {
			add(filepath.Join(d, "AppData"))
			add(filepath.Join(d, "Desktop"))
			add(filepath.Join(d, "Downloads"))
			add(filepath.Join(d, "Documents", "My Games"))
		}
	}
	if e.Documents != "" {
		add(filepath.Join(e.Documents, "My Games"))
	}
	for _, p := range e.ExtraProtected {
		add(p)
	}
	return out
}

func samePath(a, b string) bool { return strings.EqualFold(filepath.Clean(a), filepath.Clean(b)) }

// isWithin reports whether child is inside (or equal to) parent.
func isWithin(child, parent string) bool {
	child, parent = filepath.Clean(child), filepath.Clean(parent)
	if samePath(child, parent) {
		return true
	}
	rel, err := filepath.Rel(strings.ToLower(parent), strings.ToLower(child))
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// CheckSafe refuses paths that are too broad to be wiped and replaced:
// drive roots, the user folder, AppData itself, Steam's own folders, and
// anything inside PlaySwap's data folder.
func (e *Env) CheckSafe(p string) error {
	p = filepath.Clean(p)
	if filepath.Dir(p) == p {
		return fmt.Errorf("%s is a whole drive; pick the game's own folder", p)
	}
	// Need at least two levels below the volume (e.g. C:\Games\X, /tmp/x).
	vol := filepath.VolumeName(p)
	rest := strings.Trim(strings.TrimPrefix(p, vol), `\/`)
	if !strings.ContainsAny(rest, `\/`) {
		return fmt.Errorf("%s is too broad; pick the game's own folder", p)
	}
	if e.DataDir != "" && isWithin(p, e.DataDir) {
		return errors.New("that folder is inside PlaySwap's own data folder")
	}
	for _, prot := range e.protected() {
		if isWithin(prot, p) {
			return fmt.Errorf("%s is too broad (it contains %s); pick the game's own folder inside it", p, prot)
		}
	}
	return nil
}

// Tokenize rewrites an absolute path with tokens so stored configs survive
// user renames and different drives: C:\Users\me\AppData\Roaming\X -> %APPDATA%\X.
func (e *Env) Tokenize(abs string) string {
	abs = filepath.Clean(abs)
	type cand struct{ tok, val string }
	var cands []cand
	add := func(tok, val string) {
		if val != "" {
			cands = append(cands, cand{tok, filepath.Clean(val)})
		}
	}
	if e.SteamUser != nil {
		if su, err := e.SteamUser(); err == nil {
			add(TokSteamUser, su)
		}
	}
	add(TokSteam, e.SteamPath)
	add(TokDocuments, e.Documents)
	add(TokSavedGames, e.SavedGames)
	add(TokLocalLow, e.LocalLow)
	if e.Getenv != nil {
		add("%LOCALAPPDATA%", e.Getenv("LOCALAPPDATA"))
		add("%APPDATA%", e.Getenv("APPDATA"))
		add("%USERPROFILE%", e.Getenv("USERPROFILE"))
	}
	sort.SliceStable(cands, func(i, j int) bool { return len(cands[i].val) > len(cands[j].val) })
	for _, c := range cands {
		if samePath(abs, c.val) {
			return c.tok
		}
		if isWithin(abs, c.val) {
			return c.tok + abs[len(c.val):]
		}
	}
	return abs
}
