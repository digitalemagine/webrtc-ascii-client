#!/usr/bin/env bash
# Takes the README picture: termplay drawing the Big Buck Bunny frame of
# docs/RENDERER_COMPARISON.md as color sextants, in an iTerm2 window of
# 96×29 cells, captured with screencapture. macOS only.
#
# Needs: go, ffmpeg, curl, unzip, shasum, iTerm2, and Screen Recording
# permission for the terminal that runs this script.
# Usage: internal/tools/rendercompare/screenshot.sh [OUT.png] [WORKDIR]
#   OUT.png defaults to docs/images/termplay-sextant.png, WORKDIR to
#   ./rendercompare-work (shared with run.sh, so the video is downloaded once).
# The font is the one of iTerm2's default profile; name it in the caption.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../../.." && pwd)
out=$(cd "$(dirname "${1:-$repo/docs/images/termplay-sextant.png}")" && pwd)/$(basename "${1:-termplay-sextant.png}")
work=$(mkdir -p "${2:-rendercompare-work}" && cd "${2:-rendercompare-work}" && pwd)
cols=96 rows=27 # the grid of run.sh; the window has two more rows for the status line
version=${VERSION:-v0.1.0}
title="termplay -r sextant -c -w $cols -H $rows" # the window title, also used to find the window

for c in go ffmpeg curl unzip shasum osascript screencapture; do
	command -v "$c" >/dev/null || { echo "screenshot.sh: $c not found" >&2; exit 1; }
done
[[ -d /Applications/iTerm.app ]] || { echo "screenshot.sh: iTerm2 not found" >&2; exit 1; }

# The frame, as in run.sh.
cd "$work"
if [[ ! -f bbb.zip ]]; then
	curl -sSfL -o bbb.zip.part https://download.blender.org/peach/bigbuckbunny_movies/BigBuckBunny_640x360.m4v.zip
	mv bbb.zip.part bbb.zip
fi
echo "7118242b6728d40c871479c5b3c0f0fb27d748089df15d7f1b469f297c74a2d6  bbb.zip" | shasum -a 256 -c --quiet -
[[ -f BigBuckBunny_640x360.m4v ]] || unzip -q bbb.zip
mkdir -p pictures
ffmpeg -v error -nostdin -y -ss 75 -i BigBuckBunny_640x360.m4v -frames:v 1 -vf scale=640:360 pictures/bunny.png

(cd "$repo" && CGO_ENABLED=0 go build -trimpath \
	-ldflags "-X github.com/digitalemagine/webrtc-ascii-client/internal/version.Version=$version" \
	-o "$work/termplay" ./cmd/termplay)

# What runs in the window. iTerm2 starts it without the login PATH, so every
# program is named by its full path. It waits until the window has its final
# size, then shows the frame until the window is closed.
ffmpeg=$(command -v ffmpeg)
cat >"$work/screenshot-run.sh" <<EOF
#!/bin/bash
printf '\033]0;$title\007'
until [[ \$(stty size) == "$((rows + 2)) $cols" ]]; do sleep 0.1; done
cd '$work'
'$ffmpeg' -v error -nostdin -i pictures/bunny.png -pix_fmt yuv420p -f yuv4mpegpipe - |
	./termplay -r sextant -c -w $cols -H $rows -hold
EOF
chmod +x "$work/screenshot-run.sh"

osascript - "$work/screenshot-run.sh" $((rows + 2)) "$cols" <<'EOF'
on run argv
	tell application "iTerm2"
		set w to (create window with default profile command (item 1 of argv))
		tell current session of w
			set rows to (item 2 of argv) as integer
			set columns to (item 3 of argv) as integer
		end tell
	end tell
end run
EOF
trap 'osascript -e "tell application \"iTerm2\" to close (windows whose name contains \"$title\")" >/dev/null 2>&1 || true' EXIT

# The window's number for screencapture -l. AppleScript's window bounds don't
# match the screen, so the window is found by its title.
id=
for _ in $(seq 50); do
	id=$(osascript -l JavaScript - "$title" <<'EOF'
ObjC.import('CoreGraphics');
function run(argv) {
	const list = ObjC.deepUnwrap(ObjC.castRefToObject(
		$.CGWindowListCopyWindowInfo($.kCGWindowListOptionOnScreenOnly, $.kCGNullWindowID)));
	const w = list.find(w => w.kCGWindowOwnerName === 'iTerm2' && (w.kCGWindowName || '').includes(argv[0]));
	return w ? String(w.kCGWindowNumber) : '';
}
EOF
)
	[[ -n $id ]] && break
	sleep 0.2
done
[[ -n $id ]] || { echo "screenshot.sh: iTerm2 window not found (Screen Recording permission?)" >&2; exit 1; }

sleep 2 # let the frame and the status line settle
screencapture -o -x -l"$id" "$out"
echo "picture: $out"
