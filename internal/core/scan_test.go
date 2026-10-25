package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanFoldersAndRoots(t *testing.T) {
	f := newFixture(t)
	drive := filepath.Join(f.root, "D")
	games := filepath.Join(drive, "Games")
	// Unity game
	write(t, filepath.Join(games, "Hollow.Knight.v1.5.78-GOG", "hollow_knight.exe"), strings.Repeat("x", 3000))
	write(t, filepath.Join(games, "Hollow.Knight.v1.5.78-GOG", "UnityPlayer.dll"), "x")
	// Unreal game with launcher + shipping exe
	ue := filepath.Join(drive, "Stray")
	write(t, filepath.Join(ue, "Stray.exe"), strings.Repeat("x", 200))
	write(t, filepath.Join(ue, "Engine", "Binaries", "ThirdParty", "x.dll"), "x")
	write(t, filepath.Join(ue, "Hk_project", "Binaries", "Win64", "Stray-Win64-Shipping.exe"), strings.Repeat("x", 9000))
	write(t, filepath.Join(ue, "Hk_project", "Binaries", "Win64", "steam_api64.dll"), "x")
	// Not a game
	write(t, filepath.Join(drive, "Tools", "notepad2.exe"), strings.Repeat("x", 5000))
	write(t, filepath.Join(drive, "Tools", "readme.txt"), "x")
	// Steam library on the drive is skipped
	write(t, filepath.Join(drive, "SteamLibrary", "steamapps", "common", "X", "steam_api.dll"), "x")

	found := ScanFolders([]string{drive, games}, []string{drive, games})
	names := map[string]FoundGame{}
	for _, g := range found {
		names[g.Name] = g
	}
	if len(found) != 2 {
		t.Fatalf("expected 2 games, got %+v", found)
	}
	hk, ok := names["Hollow Knight"]
	if !ok || hk.ProcessName != "hollow_knight" || !strings.HasSuffix(hk.Exe, "hollow_knight.exe") {
		t.Fatalf("hollow knight: %+v", names)
	}
	st := names["Stray"]
	if !strings.HasSuffix(st.Exe, filepath.Join("Stray", "Stray.exe")) || st.ProcessName != "Stray-Win64-Shipping" {
		t.Fatalf("stray launch/process: %+v", st)
	}
	// GameRoot from the shipping exe walks up to the game folder.
	if r := GameRoot(filepath.Join(ue, "Hk_project", "Binaries", "Win64", "Stray-Win64-Shipping.exe"), []string{drive}); !samePath(r, ue) {
		t.Fatalf("GameRoot = %s", r)
	}
	// Save suggestions are filled by FinishFound
	write(t, filepath.Join(f.home, "AppData", "LocalLow", "Team Cherry", "Hollow Knight", "user1.dat"), "save")
	done := f.env.FinishFound(found)
	for _, g := range done {
		if g.Name == "Hollow Knight" {
			if !g.Ready || len(g.Paths) != 1 || g.Paths[0] != "{LOCALLOW}/Team Cherry/Hollow Knight" {
				t.Fatalf("paths: %+v", g)
			}
		}
	}
}

func TestEpicManifests(t *testing.T) {
	dir := t.TempDir()
	inst := filepath.Join(dir, "Games", "Control")
	write(t, filepath.Join(inst, "Control.exe"), "x")
	write(t, filepath.Join(dir, "m", "a.item"), `{"DisplayName":"Control","InstallLocation":"`+filepath.ToSlash(inst)+`","LaunchExecutable":"Control.exe","AppCategories":["public","games","applications"]}`)
	write(t, filepath.Join(dir, "m", "b.item"), `{"DisplayName":"Unreal Engine","InstallLocation":"/x","LaunchExecutable":"UE.exe","AppCategories":["engines"]}`)
	g := EpicGames(filepath.Join(dir, "m"))
	if len(g) != 1 || g[0].Name != "Control" || g[0].Source != "epic" {
		t.Fatalf("%+v", g)
	}
}

func TestPrettyName(t *testing.T) {
	for in, want := range map[string]string{
		"Hollow.Knight.v1.5.78-GOG": "Hollow Knight",
		"Cyberpunk 2077":            "Cyberpunk 2077",
		"Elden_Ring":                "Elden Ring",
		"Stray":                     "Stray",
	} {
		if got := prettyName(in); got != want {
			t.Errorf("prettyName(%q) = %q", in, want)
		}
	}
}

func TestProposePaths(t *testing.T) {
	s := []Suggestion{
		{Path: "a", Resolved: "/x/a", Size: 10},
		{Path: "a/sub", Resolved: "/x/a/sub", Size: 5},
		{Path: "big", Resolved: "/x/big", Size: 2 << 30},
		{Path: "b", Resolved: "/x/b", Size: 1},
	}
	got := ProposePaths(s, nil)
	if strings.Join(got, ",") != "a,b" {
		t.Fatalf("%v", got)
	}
	_ = os.Remove
}
