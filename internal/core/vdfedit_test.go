package core

import (
	"strings"
	"testing"
)

const sampleLocalConfig = "\"UserLocalConfigStore\"\n{\n\t\"Broadcast\"\n\t{\n\t\t\"Permissions\"\t\t\"1\"\n\t}\n\t\"Software\"\n\t{\n\t\t\"Valve\"\n\t\t{\n\t\t\t\"Steam\"\n\t\t\t{\n\t\t\t\t\"apps\"\n\t\t\t\t{\n\t\t\t\t\t\"730\"\n\t\t\t\t\t{\n\t\t\t\t\t\t\"LastPlayed\"\t\t\"1729700000\"\n\t\t\t\t\t\t\"LaunchOptions\"\t\t\"-novid\"\n\t\t\t\t\t}\n\t\t\t\t\t\"440\"\n\t\t\t\t\t{\n\t\t\t\t\t\t\"LastPlayed\"\t\t\"1\"\n\t\t\t\t\t}\n\t\t\t\t}\n\t\t\t\t\"LastSeen\"\t\t\"x\"\n\t\t\t}\n\t\t}\n\t}\n\t\"friends\"\n\t{\n\t\t\"a\"\t\t\"b\"\n\t}\n}\n"

func launchOpt(t *testing.T, text string, app int) string {
	t.Helper()
	m, err := ParseVDF(text)
	if err != nil {
		t.Fatalf("result doesn't parse: %v\n%s", err, text)
	}
	n := m
	for _, k := range SteamAppsPath(app) {
		n = vdfMap(n, k)
		if n == nil {
			return "<none>"
		}
	}
	return vdfStr(n, "LaunchOptions")
}

func TestSetVDFReplace(t *testing.T) {
	v := `"C:\Users\me\AppData\Local\Programs\PlaySwap\PlaySwap.exe" --steam 730 -- %command% -novid`
	out, err := SetVDFValue(sampleLocalConfig, SteamAppsPath(730), "LaunchOptions", v)
	if err != nil {
		t.Fatal(err)
	}
	if got := launchOpt(t, out, 730); got != v {
		t.Fatalf("got %q", got)
	}
	// Only that value changed.
	if strings.Replace(out, vdfQuote(v), `"-novid"`, 1) != sampleLocalConfig {
		t.Fatal("other text changed")
	}
	if !strings.Contains(out, `\"C:\\Users\\me`) {
		t.Fatal("not escaped like Steam")
	}
	// and back
	back, _ := SetVDFValue(out, SteamAppsPath(730), "LaunchOptions", "-novid")
	if back != sampleLocalConfig {
		t.Fatal("restore didn't give the original file")
	}
}

func TestSetVDFInsertKey(t *testing.T) {
	out, err := SetVDFValue(sampleLocalConfig, SteamAppsPath(440), "LaunchOptions", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if launchOpt(t, out, 440) != "abc" || launchOpt(t, out, 730) != "-novid" {
		t.Fatal("insert failed")
	}
	if !strings.Contains(out, "\t\t\t\t\t\t\"LaunchOptions\"\t\t\"abc\"\n\t\t\t\t\t}") {
		t.Fatalf("indentation off:\n%s", out)
	}
}

func TestSetVDFInsertApp(t *testing.T) {
	out, err := SetVDFValue(sampleLocalConfig, SteamAppsPath(1245620), "LaunchOptions", "x y")
	if err != nil {
		t.Fatal(err)
	}
	if launchOpt(t, out, 1245620) != "x y" || launchOpt(t, out, 440) != "" {
		t.Fatalf("insert app failed:\n%s", out)
	}
	m, _ := ParseVDF(out)
	if vdfStr(vdfMap(vdfMap(m, "UserLocalConfigStore"), "friends"), "a") != "b" {
		t.Fatal("unrelated data lost")
	}
}

func TestSetVDFMissingTree(t *testing.T) {
	out, err := SetVDFValue("\"UserLocalConfigStore\"\n{\n}\n", SteamAppsPath(730), "LaunchOptions", "q")
	if err != nil || launchOpt(t, out, 730) != "q" {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = SetVDFValue("", SteamAppsPath(730), "LaunchOptions", "q")
	if err != nil || launchOpt(t, out, 730) != "q" {
		t.Fatalf("empty file: %v\n%s", err, out)
	}
	if _, err := SetVDFValue("\"a\" { \"b\" ", SteamAppsPath(730), "LaunchOptions", "q"); err == nil {
		t.Fatal("broken file should error")
	}
}
