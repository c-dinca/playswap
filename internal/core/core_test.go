package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

type fixture struct {
	root, home, appdata, steam string
	env                        *Env
	eng                        *Engine
	cfg                        *Config
}

func newFixture(t *testing.T) *fixture {
	root := t.TempDir()
	f := &fixture{root: root}
	f.home = filepath.Join(root, "Users", "me")
	f.appdata = filepath.Join(f.home, "AppData", "Roaming")
	f.steam = filepath.Join(root, "Steam")
	vars := map[string]string{
		"APPDATA":      f.appdata,
		"LOCALAPPDATA": filepath.Join(f.home, "AppData", "Local"),
		"USERPROFILE":  f.home,
	}
	f.env = &Env{
		Getenv:     func(k string) string { return vars[strings.ToUpper(k)] },
		Home:       f.home,
		Documents:  filepath.Join(f.home, "Documents"),
		SavedGames: filepath.Join(f.home, "Saved Games"),
		LocalLow:   filepath.Join(f.home, "AppData", "LocalLow"),
		SteamPath:  f.steam,
		SteamUser:  func() (string, error) { return SteamUserDir(f.steam, "", 0) },
		DataDir:    filepath.Join(root, "PlaySwapData"),
	}
	_ = os.MkdirAll(filepath.Join(f.steam, "userdata", "12345"), 0o755)
	store := &Store{Dir: f.env.DataDir}
	f.eng = &Engine{Store: store, Env: f.env}
	f.cfg = DefaultConfig()
	f.cfg.Games = []Game{{
		ID: "cs2", Name: "Counter-Strike 2", ProcessName: "cs2",
		Launch: Launch{Type: "steam", AppID: 730},
		Paths: []PathEntry{
			{ID: "cfg", Path: "{STEAMUSER}/730/local/cfg"},
			{ID: "opt", Path: "%APPDATA%/Game/options.ini"},
		},
	}}
	return f
}

func (f *fixture) cfgFile() string {
	return filepath.Join(f.steam, "userdata", "12345", "730", "local", "cfg", "video.txt")
}
func (f *fixture) optFile() string { return filepath.Join(f.appdata, "Game", "options.ini") }

func TestSwapFlow(t *testing.T) {
	f := newFixture(t)
	write(t, f.cfgFile(), "res=2560x1440")
	write(t, f.optFile(), "sens=1.2")

	// First time: needs an owner.
	err := f.eng.Activate(f.cfg, "cs2", "p2", ActivateOptions{})
	if !errors.Is(err, ErrNeedOwner) {
		t.Fatalf("want ErrNeedOwner, got %v", err)
	}
	// Owner p1; p2 is new -> needs a mode.
	err = f.eng.Activate(f.cfg, "cs2", "p2", ActivateOptions{FirstOwner: "p1"})
	if !errors.Is(err, ErrNeedNewMode) {
		t.Fatalf("want ErrNeedNewMode, got %v", err)
	}
	if got := f.eng.Store.LoadState()["cs2"]; got != "p1" {
		t.Fatalf("owner should be recorded as p1, got %q", got)
	}
	// p2 starts fresh.
	if err := f.eng.Activate(f.cfg, "cs2", "p2", ActivateOptions{NewProfile: NewFresh}); err != nil {
		t.Fatal(err)
	}
	if read(t, f.cfgFile()) != "<missing>" || read(t, f.optFile()) != "<missing>" {
		t.Fatal("fresh start should clear live files")
	}
	// p2 plays and the game writes settings.
	write(t, f.cfgFile(), "res=1920x1080")
	write(t, f.optFile(), "sens=3.0")
	if err := f.eng.FinishSession(f.cfg, "cs2", "p2"); err != nil {
		t.Fatal(err)
	}
	// Swap back to p1.
	if err := f.eng.Activate(f.cfg, "cs2", "p1", ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	if read(t, f.cfgFile()) != "res=2560x1440" || read(t, f.optFile()) != "sens=1.2" {
		t.Fatalf("p1 files not restored: %q %q", read(t, f.cfgFile()), read(t, f.optFile()))
	}
	// p1 changes something without going through FinishSession (e.g. app was closed),
	// then p2 is selected: p1's change must be saved first.
	write(t, f.optFile(), "sens=1.5")
	if err := f.eng.Activate(f.cfg, "cs2", "p2", ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	if read(t, f.cfgFile()) != "res=1920x1080" || read(t, f.optFile()) != "sens=3.0" {
		t.Fatal("p2 files not restored")
	}
	if err := f.eng.Activate(f.cfg, "cs2", "p1", ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	if read(t, f.optFile()) != "sens=1.5" {
		t.Fatalf("p1's unsaved change was lost: %q", read(t, f.optFile()))
	}
	// Backups exist and are pruned.
	if n := len(f.eng.Backups("cs2")); n < 3 {
		t.Fatalf("expected backups, got %d", n)
	}
	for i := 0; i < 15; i++ {
		_, _ = f.eng.Backup(f.cfg.Game("cs2"), 4)
	}
	if n := len(f.eng.Backups("cs2")); n != 4 {
		t.Fatalf("expected 4 backups after pruning, got %d", n)
	}
	// No leftovers from the atomic swap.
	_ = filepath.Walk(f.root, func(p string, _ os.FileInfo, _ error) error {
		if strings.HasSuffix(p, tmpSuffix) || strings.HasSuffix(p, oldSuffix) {
			t.Errorf("leftover %s", p)
		}
		return nil
	})
}

func TestNewProfileCopy(t *testing.T) {
	f := newFixture(t)
	write(t, f.optFile(), "sens=1.2")
	if err := f.eng.Activate(f.cfg, "cs2", "p2", ActivateOptions{FirstOwner: "p1", NewProfile: NewCopy}); err != nil {
		t.Fatal(err)
	}
	if read(t, f.optFile()) != "sens=1.2" {
		t.Fatal("copy should keep live files")
	}
	write(t, f.optFile(), "sens=9")
	if err := f.eng.Activate(f.cfg, "cs2", "p1", ActivateOptions{}); err != nil {
		t.Fatal(err)
	}
	if read(t, f.optFile()) != "sens=1.2" {
		t.Fatal("p1 should keep the original")
	}
}

func TestOwnerNobody(t *testing.T) {
	f := newFixture(t)
	write(t, f.optFile(), "someone")
	if err := f.eng.Activate(f.cfg, "cs2", "p1", ActivateOptions{FirstOwner: OwnerNobody, NewProfile: NewFresh}); err != nil {
		t.Fatal(err)
	}
	if read(t, f.optFile()) != "<missing>" {
		t.Fatal("expected cleared")
	}
	if len(f.eng.Backups("cs2")) != 1 {
		t.Fatal("unknown files must be backed up")
	}
}

func TestBadPathTouchesNothing(t *testing.T) {
	f := newFixture(t)
	write(t, f.optFile(), "keep")
	f.cfg.Games[0].Paths = append(f.cfg.Games[0].Paths, PathEntry{ID: "bad", Path: "%NOPE%/x"})
	err := f.eng.Activate(f.cfg, "cs2", "p1", ActivateOptions{FirstOwner: OwnerNobody, NewProfile: NewFresh})
	if err == nil || !strings.Contains(err.Error(), "NOPE") {
		t.Fatalf("expected env error, got %v", err)
	}
	if read(t, f.optFile()) != "keep" {
		t.Fatal("nothing should have been touched")
	}
}

func TestCheckSafe(t *testing.T) {
	f := newFixture(t)
	bad := []string{
		"%APPDATA%",
		"%USERPROFILE%",
		"%USERPROFILE%/AppData",
		"{DOCUMENTS}",
		"{DOCUMENTS}/My Games",
		"{STEAM}",
		"{STEAMUSER}",
		f.home,
		f.root,
		filepath.Join(f.env.DataDir, "x"),
		"/",
		"/tmp",
		"relative/path",
	}
	for _, p := range bad {
		if r, err := f.env.Resolve(p); err == nil {
			t.Errorf("%s resolved to %s, expected refusal", p, r)
		}
	}
	good := []string{"%APPDATA%/Game", "{STEAMUSER}/730/local/cfg", "{DOCUMENTS}/My Games/Skyrim", "{STEAMUSER}/730"}
	for _, p := range good {
		if _, err := f.env.Resolve(p); err != nil {
			t.Errorf("%s refused: %v", p, err)
		}
	}
	// Overlapping entries are refused.
	g := f.cfg.Games[0]
	g.Paths = []PathEntry{{ID: "a", Path: "%APPDATA%/Game"}, {ID: "b", Path: "%APPDATA%/Game/sub"}}
	if err := f.eng.CheckPaths(&g); err == nil {
		t.Error("overlapping paths should be refused")
	}
}

func TestTokenize(t *testing.T) {
	f := newFixture(t)
	cases := map[string]string{
		filepath.Join(f.appdata, "Game"):                            "%APPDATA%/Game",
		filepath.Join(f.home, "AppData", "LocalLow", "Co", "G"):     "{LOCALLOW}/Co/G",
		filepath.Join(f.home, "AppData", "Local", "G"):              "%LOCALAPPDATA%/G",
		filepath.Join(f.steam, "userdata", "12345", "730", "local"): "{STEAMUSER}/730/local",
		filepath.Join(f.home, "Documents", "My Games", "X"):         "{DOCUMENTS}/My Games/X",
	}
	for in, want := range cases {
		if got := f.env.Tokenize(in); got != want {
			t.Errorf("Tokenize(%s) = %s, want %s", in, got, want)
		}
		if back, err := f.env.Resolve(f.env.Tokenize(in)); err != nil || back != in {
			t.Errorf("round trip %s -> %s (%v)", in, back, err)
		}
	}
}

func TestVDFAndSteam(t *testing.T) {
	f := newFixture(t)
	lib2 := filepath.Join(f.root, "D", "SteamLibrary")
	write(t, filepath.Join(f.steam, "steamapps", "libraryfolders.vdf"), `"libraryfolders"
{
	"0" { "path" "`+strings.ReplaceAll(f.steam, `\`, `\\`)+`" "apps" { "730" "1" } }
	"1" { "path" "`+strings.ReplaceAll(lib2, `\`, `\\`)+`" }
}`)
	write(t, filepath.Join(f.steam, "steamapps", "appmanifest_730.acf"), `"AppState" { "appid" "730" "name" "Counter-Strike 2" "installdir" "Counter-Strike Global Offensive" }`)
	write(t, filepath.Join(lib2, "steamapps", "appmanifest_1245620.acf"), `"AppState"
{
	// comment
	"appid"		"1245620"
	"name"		"ELDEN RING"
	"installdir"		"ELDEN RING"
}`)
	write(t, filepath.Join(lib2, "steamapps", "appmanifest_228980.acf"), `"AppState" { "appid" "228980" "name" "Steamworks Common Redistributables" "installdir" "x" }`)
	games := InstalledSteamGames(f.steam)
	if len(games) != 2 || games[0].Name != "Counter-Strike 2" || games[1].AppID != 1245620 {
		t.Fatalf("unexpected games: %+v", games)
	}
	if !strings.HasPrefix(games[1].InstallDir, lib2) {
		t.Fatalf("wrong install dir %s", games[1].InstallDir)
	}

	// Exe candidates & suggestions
	inst := games[1].InstallDir
	write(t, filepath.Join(inst, "Game", "eldenring.exe"), strings.Repeat("x", 5000))
	write(t, filepath.Join(inst, "Game", "start_protected_game.exe"), "xx")
	write(t, filepath.Join(inst, "Game", "UnityCrashHandler64.exe"), strings.Repeat("x", 9000))
	c := ProcessCandidates(inst)
	if len(c) != 2 || c[0].Name != "eldenring" {
		t.Fatalf("candidates: %+v", c)
	}
	if FindExe(inst, "eldenring") == "" {
		t.Fatal("FindExe failed")
	}
	write(t, filepath.Join(f.appdata, "EldenRing", "7656", "ER0000.sl2"), "save")
	write(t, filepath.Join(f.home, "AppData", "LocalLow", "Some Studio", "Elden Ring Thing", "x"), "y")
	sug := f.env.SuggestPaths("ELDEN RING", "ELDEN RING", 1245620)
	if len(sug) < 2 || sug[0].Path != "%APPDATA%/EldenRing" {
		t.Fatalf("suggestions: %+v", sug)
	}
}

func TestSteamUserDir(t *testing.T) {
	f := newFixture(t)
	_ = os.MkdirAll(filepath.Join(f.steam, "userdata", "999"), 0o755)
	if p, _ := SteamUserDir(f.steam, "", 999); filepath.Base(p) != "999" {
		t.Fatalf("active user should win, got %s", p)
	}
	if p, _ := SteamUserDir(f.steam, "12345", 999); filepath.Base(p) != "12345" {
		t.Fatalf("configured should win, got %s", p)
	}
	if _, err := SteamUserDir(f.steam, "42", 0); err == nil {
		t.Fatal("missing configured account should error")
	}
}

func TestValidate(t *testing.T) {
	ps := []Player{{Name: " Alex "}, {Name: "alex"}}
	if err := ValidatePlayers(ps); err == nil {
		t.Fatal("duplicate names should fail")
	}
	ps = []Player{{Name: "Alex"}, {Name: "Sam", Color: "red"}}
	if err := ValidatePlayers(ps); err != nil || ps[0].ID == "" || ps[1].Color != PlayerColors[1] {
		t.Fatalf("validate: %v %+v", err, ps)
	}
	g := Game{ID: "g1", Name: "X", Launch: Launch{Type: "exe", Exe: `C:\x.exe`}, ProcessName: "X.exe", Paths: []PathEntry{{Path: "%APPDATA%/X"}}}
	if err := ValidateGame(&g); err != nil || g.ProcessName != "X" || g.Paths[0].ID == "" {
		t.Fatalf("validate game: %v %+v", err, g)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	cfg, first, err := s.LoadConfig()
	if err != nil || !first || len(cfg.Players) != 2 {
		t.Fatalf("defaults: %v %v", first, err)
	}
	cfg.Players[0].Name = "Alex"
	if err := s.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	cfg2, first, _ := s.LoadConfig()
	if first || cfg2.Players[0].Name != "Alex" || !cfg2.Settings.CloseToTray {
		t.Fatal("round trip failed")
	}
	write(t, s.ConfigPath(), "{broken")
	if _, first, err := s.LoadConfig(); !first || err == nil {
		t.Fatal("broken config should reset with an error")
	}
	if !exists(s.ConfigPath() + ".broken") {
		t.Fatal("broken config should be kept")
	}
}

func TestSteamLaunchOptions(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "config", "localconfig.vdf"), `"UserLocalConfigStore"
{
	"Software"
	{
		"Valve"
		{
			"Steam"
			{
				"apps"
				{
					"730"
					{
						"LaunchOptions"		"-novid -w 1920"
					}
					"440" { "LaunchOptions" "\"C:\\Users\\me\\AppData\\Local\\Programs\\PlaySwap\\PlaySwap.exe\" --steam 440 -- %command%" }
				}
			}
		}
	}
}`)
	got, err := SteamLaunchOptions(dir, 730)
	if err != nil || got != "-novid -w 1920" {
		t.Fatalf("got %q %v", got, err)
	}
	opt := WrapperLaunchOption(`C:\P S\PlaySwap.exe`, 730, got)
	if opt != `"C:\P S\PlaySwap.exe" --steam 730 -- %command% -novid -w 1920` {
		t.Fatalf("option %q", opt)
	}
	if HasWrapper(got, 730) {
		t.Fatal("should not be detected")
	}
	o440, _ := SteamLaunchOptions(dir, 440)
	if !HasWrapper(o440, 440) || !strings.HasPrefix(o440, `"C:\Users\me`) {
		t.Fatalf("440: %q", o440)
	}
	if g, _ := SteamLaunchOptions(dir, 999); g != "" {
		t.Fatal("missing app should be empty")
	}
}
