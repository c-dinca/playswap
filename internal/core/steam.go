package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// ---------------------------------------------------------------- VDF -----

// ParseVDF parses Valve's KeyValues text format into nested maps.
// Keys are lower-cased. Values are string or map[string]any.
func ParseVDF(s string) (map[string]any, error) {
	toks, err := vdfTokens(s)
	if err != nil {
		return nil, err
	}
	pos := 0
	var parse func() (map[string]any, error)
	parse = func() (map[string]any, error) {
		m := map[string]any{}
		for pos < len(toks) {
			t := toks[pos]
			if t.kind == '}' {
				pos++
				return m, nil
			}
			if t.kind != 's' {
				return nil, errors.New("vdf: expected key")
			}
			key := strings.ToLower(t.val)
			pos++
			if pos >= len(toks) {
				return nil, errors.New("vdf: missing value")
			}
			switch toks[pos].kind {
			case '{':
				pos++
				sub, err := parse()
				if err != nil {
					return nil, err
				}
				m[key] = sub
			case 's':
				m[key] = toks[pos].val
				pos++
			default:
				return nil, errors.New("vdf: unexpected }")
			}
		}
		return m, nil
	}
	return parse()
}

type vdfTok struct {
	kind byte // 's', '{', '}'
	val  string
}

func vdfTokens(s string) ([]vdfTok, error) {
	var out []vdfTok
	r := []rune(s)
	for i := 0; i < len(r); {
		c := r[i]
		switch {
		case unicode.IsSpace(c):
			i++
		case c == '/' && i+1 < len(r) && r[i+1] == '/':
			for i < len(r) && r[i] != '\n' {
				i++
			}
		case c == '{' || c == '}':
			out = append(out, vdfTok{kind: byte(c)})
			i++
		case c == '"':
			i++
			var b strings.Builder
			for i < len(r) && r[i] != '"' {
				if r[i] == '\\' && i+1 < len(r) {
					i++
					switch r[i] {
					case 'n':
						b.WriteRune('\n')
					case 't':
						b.WriteRune('\t')
					default:
						b.WriteRune(r[i])
					}
				} else {
					b.WriteRune(r[i])
				}
				i++
			}
			if i >= len(r) {
				return nil, errors.New("vdf: unterminated string")
			}
			i++
			out = append(out, vdfTok{kind: 's', val: b.String()})
		case c == '[':
			// conditional like [$WIN32]; skip
			for i < len(r) && r[i] != ']' {
				i++
			}
			i++
		default:
			start := i
			for i < len(r) && !unicode.IsSpace(r[i]) && r[i] != '{' && r[i] != '}' && r[i] != '"' {
				i++
			}
			out = append(out, vdfTok{kind: 's', val: string(r[start:i])})
		}
	}
	return out, nil
}

func vdfMap(m map[string]any, key string) map[string]any {
	if v, ok := m[strings.ToLower(key)].(map[string]any); ok {
		return v
	}
	return nil
}

func vdfStr(m map[string]any, key string) string {
	if v, ok := m[strings.ToLower(key)].(string); ok {
		return v
	}
	return ""
}

// ------------------------------------------------------------ libraries ---

type SteamGame struct {
	AppID      int    `json:"appId"`
	Name       string `json:"name"`
	InstallDir string `json:"installDir"`
}

// SteamLibraries lists every steamapps library folder (including the main one).
func SteamLibraries(steamPath string) []string {
	if steamPath == "" {
		return nil
	}
	libs := []string{steamPath}
	b, err := os.ReadFile(filepath.Join(steamPath, "steamapps", "libraryfolders.vdf"))
	if err == nil {
		if m, err := ParseVDF(string(b)); err == nil {
			if lf := vdfMap(m, "libraryfolders"); lf != nil {
				keys := make([]string, 0, len(lf))
				for k := range lf {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					switch v := lf[k].(type) {
					case map[string]any:
						if p := vdfStr(v, "path"); p != "" {
							libs = append(libs, p)
						}
					case string: // very old format: "1" "D:\\SteamLibrary"
						if _, err := strconv.Atoi(k); err == nil {
							libs = append(libs, v)
						}
					}
				}
			}
		}
	}
	// de-dup
	seen := map[string]bool{}
	out := libs[:0]
	for _, l := range libs {
		k := strings.ToLower(filepath.Clean(l))
		if !seen[k] {
			seen[k] = true
			out = append(out, filepath.Clean(l))
		}
	}
	return out
}

var skipSteamApps = map[int]bool{228980: true} // Steamworks Common Redistributables

// InstalledSteamGames reads appmanifest_*.acf from every library.
func InstalledSteamGames(steamPath string) []SteamGame {
	var out []SteamGame
	seen := map[int]bool{}
	for _, lib := range SteamLibraries(steamPath) {
		apps := filepath.Join(lib, "steamapps")
		ms, _ := filepath.Glob(filepath.Join(apps, "appmanifest_*.acf"))
		for _, mf := range ms {
			b, err := os.ReadFile(mf)
			if err != nil {
				continue
			}
			m, err := ParseVDF(string(b))
			if err != nil {
				continue
			}
			st := vdfMap(m, "AppState")
			if st == nil {
				continue
			}
			id, _ := strconv.Atoi(vdfStr(st, "appid"))
			name := vdfStr(st, "name")
			dir := vdfStr(st, "installdir")
			if id == 0 || name == "" || seen[id] || skipSteamApps[id] {
				continue
			}
			if strings.Contains(strings.ToLower(name), "redistributable") || strings.HasPrefix(name, "Steamworks") {
				continue
			}
			seen[id] = true
			out = append(out, SteamGame{AppID: id, Name: name, InstallDir: filepath.Join(apps, "common", dir)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// SteamAccounts lists account folders in Steam\userdata.
func SteamAccounts(steamPath string) []string {
	ents, err := os.ReadDir(filepath.Join(steamPath, "userdata"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() || e.Name() == "0" {
			continue
		}
		if _, err := strconv.ParseUint(e.Name(), 10, 32); err == nil {
			out = append(out, e.Name())
		}
	}
	return out
}

// SteamUserDir picks Steam\userdata\<id>: the configured account, else the
// logged-in one, else the only one, else the most recently used.
func SteamUserDir(steamPath, configured string, activeUser uint32) (string, error) {
	if steamPath == "" {
		return "", errors.New("Steam isn't installed (or wasn't found)")
	}
	ud := filepath.Join(steamPath, "userdata")
	if configured != "" {
		p := filepath.Join(ud, configured)
		if !exists(p) {
			return "", fmt.Errorf("Steam account folder %s doesn't exist; check Settings", configured)
		}
		return p, nil
	}
	if activeUser != 0 {
		p := filepath.Join(ud, strconv.FormatUint(uint64(activeUser), 10))
		if exists(p) {
			return p, nil
		}
	}
	accts := SteamAccounts(steamPath)
	switch len(accts) {
	case 0:
		return "", errors.New("no Steam account folder found; launch Steam and sign in once")
	case 1:
		return filepath.Join(ud, accts[0]), nil
	}
	best, bestT := "", int64(0)
	for _, a := range accts {
		if st, err := os.Stat(filepath.Join(ud, a)); err == nil && st.ModTime().Unix() > bestT {
			best, bestT = a, st.ModTime().Unix()
		}
	}
	return filepath.Join(ud, best), nil
}

// ------------------------------------------------------------- presets ----

type Preset struct {
	ProcessName string   `json:"processName"`
	Paths       []string `json:"paths"`
	Note        string   `json:"note,omitempty"`
}

// KnownSteamGames are games whose settings locations we know for sure.
var KnownSteamGames = map[int]Preset{
	730: {
		ProcessName: "cs2",
		Paths:       []string{`{STEAMUSER}\730\local\cfg`},
		Note:        "Covers resolution, video settings, sensitivity, crosshair and binds.",
	},
}

// SteamLaunchOptions reads a game's launch options from
// Steam\userdata\<id>\config\localconfig.vdf (read-only).
func SteamLaunchOptions(steamUserDir string, appID int) (string, error) {
	b, err := os.ReadFile(filepath.Join(steamUserDir, "config", "localconfig.vdf"))
	if err != nil {
		return "", err
	}
	return LaunchOptionsFromText(string(b), appID)
}

// LaunchOptionsFromText reads a game's launch options from localconfig.vdf text.
func LaunchOptionsFromText(text string, appID int) (string, error) {
	if text == "" {
		return "", nil
	}
	m, err := ParseVDF(text)
	if err != nil {
		return "", err
	}
	node := m
	for _, k := range []string{"UserLocalConfigStore", "Software", "Valve", "Steam", "apps", strconv.Itoa(appID)} {
		node = vdfMap(node, k)
		if node == nil {
			return "", nil
		}
	}
	return vdfStr(node, "LaunchOptions"), nil
}

// WrapperLaunchOption is what goes in a Steam game's launch options so that
// Steam starts PlaySwap first. Options the user already had are kept after it.
func WrapperLaunchOption(exe string, appID int, existing string) string {
	opt := fmt.Sprintf(`"%s" --steam %d -- %%command%%`, exe, appID)
	existing = strings.TrimSpace(existing)
	if existing != "" && !strings.Contains(strings.ToLower(existing), "%command%") {
		opt += " " + existing
	}
	return opt
}

// HasWrapper reports whether launch options already route through PlaySwap.
func HasWrapper(opts string, appID int) bool {
	l := strings.ToLower(opts)
	return strings.Contains(l, "playswap") && strings.Contains(l, fmt.Sprintf("--steam %d", appID)) && strings.Contains(l, "%command%")
}
