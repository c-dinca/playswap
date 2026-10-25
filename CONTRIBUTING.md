# Contributing to PlaySwap

Thanks for helping. Here is how to make a change land smoothly.

## Reporting a bug

Open an issue using the bug report form. Include:

- Your Windows version and the PlaySwap version (Settings > About).
- The game, and whether it was started from Steam, PlaySwap or a shortcut.
- `%LOCALAPPDATA%\PlaySwap\playswap.log`. It has no passwords, but it does contain folder paths, so check it before you share it.

## Adding a game preset

A preset lets PlaySwap set a game up with no questions. Presets live in `KnownSteamGames` in `internal/core/steam.go`:

```go
730: {
    ProcessName: "cs2",
    Paths:       []string{`{STEAMUSER}\730\local\cfg`},
    Note:        "Covers resolution, video settings, sensitivity, crosshair and binds.",
},
```

- The key is the Steam app id (the number in the game's store URL).
- `ProcessName` is the game's process while it runs, without `.exe` (Task Manager > Details).
- `Paths` lists every settings and save location, using the path tokens from the README.
- Please say in the pull request how you checked the locations, for example by testing a swap or citing PCGamingWiki.

## Code changes

1. Fork the repo and create a branch.
2. Make the change. Keep platform-neutral logic in `internal/core`, with tests.
3. Run `go test ./internal/core/` and `./build.sh`.
4. Open a pull request saying what changed and how you tested it on Windows.

The interface is plain HTML, CSS and JavaScript in `internal/app/ui`, embedded into the exe at build time.
