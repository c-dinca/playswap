#!/usr/bin/env bash
# Builds dist/PlaySwap-Setup.exe (and the bare dist/PlaySwap.exe) for Windows x64.
# Requires Go 1.24+ and Python 3 (Pillow optional, only to redraw the icon).
# Runs on Linux, macOS or Windows (Git Bash). Version: $VERSION, else the VERSION file.
set -euo pipefail
cd "$(dirname "$0")"
VERSION="${VERSION:-$(cat VERSION)}"
export GOOS=windows GOARCH=amd64 CGO_ENABLED=0
LDFLAGS="-H windowsgui -s -w"

# Redraw the icon when Pillow is available; otherwise use the committed one.
if python3 -c "import PIL" 2>/dev/null; then
  python3 tools/mkicon.py assets/playswap.ico
fi
python3 tools/mkres.py cmd/playswap/rsrc_windows_amd64.syso assets/playswap.ico "PlaySwap" "PlaySwap" "$VERSION" PlaySwap.exe
python3 tools/mkres.py cmd/setup/rsrc_windows_amd64.syso assets/playswap.ico "PlaySwap" "PlaySwap Setup" "$VERSION" PlaySwap-Setup.exe

GOOS= GOARCH= go test ./internal/core/

mkdir -p dist cmd/setup/payload
go build -trimpath -ldflags "$LDFLAGS -X playswap/internal/app.Version=$VERSION" -o dist/PlaySwap.exe ./cmd/playswap
python3 tools/checkpe.py dist/PlaySwap.exe >/dev/null

cp dist/PlaySwap.exe cmd/setup/payload/PlaySwap.exe
cp assets/THIRD_PARTY_NOTICES.txt cmd/setup/payload/THIRD_PARTY_NOTICES.txt
go build -trimpath -ldflags "$LDFLAGS -X main.Version=$VERSION" -o dist/PlaySwap-Setup.exe ./cmd/setup
python3 tools/checkpe.py dist/PlaySwap-Setup.exe >/dev/null

ls -la dist
