package core

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Suggestion struct {
	Path     string `json:"path"`     // tokenized, ready to store
	Resolved string `json:"resolved"` // absolute
	Reason   string `json:"reason"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}

func norm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func nameMatches(dirName string, keys []string) bool {
	d := norm(dirName)
	if len(d) < 3 {
		return false
	}
	for _, k := range keys {
		if k == "" {
			continue
		}
		if d == k {
			return true
		}
		if len(d) >= 5 && len(k) >= 5 && (strings.Contains(k, d) || strings.Contains(d, k)) {
			return true
		}
	}
	return false
}

// SuggestPaths looks in the usual save/config locations for folders named
// like the game.
func (e *Env) SuggestPaths(gameName, installDirName string, appID int) []Suggestion {
	keys := []string{norm(gameName), norm(installDirName)}
	// "Counter-Strike 2" -> also try without trailing numbers/editions
	if f := strings.Fields(gameName); len(f) > 1 {
		keys = append(keys, norm(strings.Join(f[:len(f)-1], " ")))
	}
	if strings.Contains(gameName, ":") {
		keys = append(keys, norm(strings.SplitN(gameName, ":", 2)[0]))
	}

	type root struct {
		path, label string
		depth       int
	}
	var roots []root
	add := func(p, label string, depth int) {
		if p != "" {
			roots = append(roots, root{p, label, depth})
		}
	}
	if e.Documents != "" {
		add(filepath.Join(e.Documents, "My Games"), `Documents\My Games`, 2)
		add(e.Documents, "Documents", 2)
	}
	add(e.SavedGames, "Saved Games", 2)
	if e.Getenv != nil {
		add(e.Getenv("APPDATA"), `AppData\Roaming`, 2)
		add(e.Getenv("LOCALAPPDATA"), `AppData\Local`, 2)
	}
	add(e.LocalLow, `AppData\LocalLow`, 2)

	seen := map[string]bool{}
	var out []Suggestion
	push := func(abs, reason string) {
		k := strings.ToLower(filepath.Clean(abs))
		if seen[k] {
			return
		}
		if e.CheckSafe(abs) != nil {
			return
		}
		seen[k] = true
		s := Suggestion{Path: e.Tokenize(abs), Resolved: abs, Reason: reason, Size: Size(abs)}
		if st, err := os.Stat(abs); err == nil {
			s.Modified = st.ModTime().Format(time.RFC3339)
		}
		out = append(out, s)
	}

	if appID > 0 && e.SteamUser != nil {
		if su, err := e.SteamUser(); err == nil {
			p := filepath.Join(su, strconv.Itoa(appID))
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				push(p, "Steam user data for this game")
			}
		}
	}

	for _, r := range roots {
		lvl1, err := os.ReadDir(r.path)
		if err != nil {
			continue
		}
		for _, d := range lvl1 {
			if !d.IsDir() {
				continue
			}
			p1 := filepath.Join(r.path, d.Name())
			if nameMatches(d.Name(), keys) {
				push(p1, "Found in "+r.label)
				continue
			}
			if r.depth < 2 {
				continue
			}
			// publisher\game
			lvl2, err := os.ReadDir(p1)
			if err != nil || len(lvl2) > 200 {
				continue
			}
			for _, d2 := range lvl2 {
				if d2.IsDir() && nameMatches(d2.Name(), keys) {
					push(filepath.Join(p1, d2.Name()), "Found in "+r.label+`\`+d.Name())
				}
			}
		}
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

type ExeCandidate struct {
	Name    string `json:"name"` // process name without .exe
	RelPath string `json:"relPath"`
	Size    int64  `json:"size"`
}

var helperExe = []string{"crash", "unins", "redist", "setup", "install", "helper", "report", "vc_", "vcredist",
	"dxsetup", "dotnet", "prereq", "easyanticheat", "eac", "battleye", "be_service", "cefsharp", "updater",
	"uploader", "handler", "benchmark", "touchup", "cleanup", "webhelper", "ue4", "dxwebsetup", "oalinst", "physx"}

// ProcessCandidates lists .exe files in a game folder that look like the game
// itself, biggest first.
func ProcessCandidates(installDir string) []ExeCandidate {
	var out []ExeCandidate
	base := strings.Count(filepath.Clean(installDir), string(filepath.Separator))
	_ = filepath.WalkDir(installDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if strings.Count(filepath.Clean(p), string(filepath.Separator))-base >= 4 {
				return filepath.SkipDir
			}
			ln := strings.ToLower(d.Name())
			if ln == "_commonredist" || ln == "redist" || ln == "redistributables" || ln == "directx" || ln == "support" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".exe") {
			return nil
		}
		ln := strings.ToLower(d.Name())
		for _, h := range helperExe {
			if strings.Contains(ln, h) {
				return nil
			}
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(installDir, p)
		out = append(out, ExeCandidate{Name: strings.TrimSuffix(d.Name(), filepath.Ext(d.Name())), RelPath: rel, Size: info.Size()})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Size > out[j].Size })
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

// FindExe looks for <processName>.exe inside a folder (a few levels deep).
func FindExe(dir, processName string) string {
	var found string
	base := strings.Count(filepath.Clean(dir), string(filepath.Separator))
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || found != "" {
			return filepath.SkipDir
		}
		if d.IsDir() && strings.Count(filepath.Clean(p), string(filepath.Separator))-base >= 5 {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.EqualFold(d.Name(), processName+".exe") {
			found = p
			return filepath.SkipAll
		}
		return nil
	})
	return found
}
