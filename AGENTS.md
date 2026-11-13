# AGENTS.md

Notes for coding agents working on this repository. The docs for people come first: README.md,
CONTRIBUTING.md and docs/. Read docs/ARCHITECTURE.md before changing the pipeline, signaling or shutdown.
This file only lists what is easy to get wrong.

## Commands

Needs Go 1.27+ and the FFmpeg development libraries (`make check-deps`).

```bash
make build                    # the three commands into bin/, with debug info
go test -race ./...           # what CI runs
make lint                     # golangci-lint; on a Go version mismatch run make lint-install
go test ./encode -update      # after an intended encoder change; review the golden diff
CGO_ENABLED=0 go test ./cmd/webrtc-recv ./internal/client ./internal/h264rtp   # cgo boundary
```

## Rules

- **stdout carries only video** (frames, or `webrtc-recv`'s H.264). Log with `log/slog`; never `fmt.Print*`
  or `log.Printf` in library code. Warn/Error show in the status line and count as warnings; Info/Debug
  reach stderr only with `-V 1`/`-V 2`, so pick the level accordingly.
- **Return errors to `main`**, never `os.Exit`: the display has to restore the terminal.
- **One goroutine owns each resource** (track handler: its `client.Track`; encode stage: the encoder;
  display: stdout and terminal state). Hand frames over with `latest.Slot`. `Stop()` methods are
  idempotent and safe without `Start()`; producers own their channels.
- **cgo only in `internal/decoder`.** `internal/client`, `internal/h264rtp`, `encode`, `termplay` and
  `webrtc-recv` stay pure Go.
- **`encode` is public:** keep its API and package docs in shape. Encoders are pure; an image that isn't
  exactly `Config.PixelSize()` returns `dst` unchanged (normal right after a reconfiguration). Output must be
  byte-identical on every platform: integer luma, and `float64(x*y)` around products that feed additions
  (Go may fuse multiply-add on arm64). When output changes, also rerun `go run ./cmd/bandwidth-analyzer`, and
  for sextant/braille `internal/tools/rendercompare/run.sh`, and update the docs from them.
- **Units:** text renderers are sized in characters, sixel/iterm2 in pixels. Go through `encode.Config`
  (`PixelSize`, `Rows`, `IsImageMode`); the controls keep sizes in cells (about 10×20 px per cell).
- **Tests:** `t.Parallel()` where safe; `testing/synctest` instead of sleeps; names like
  `TestComponentBehavior`.
- **Docs and commits:** docs change with the behavior they describe; add a CHANGELOG `[Unreleased]` entry
  for anything a user would notice; commit messages `area: what changed`. American English ("color",
  "canceled"). Write plainly and modestly, as one person describing their own project: no marketing
  words, no claims without a measurement anyone can rerun.

## Traps

- **Janus:** every message gets a transaction ID, but only requests that wait for a reply register a
  channel. An `ack` never completes a transaction (watch replies later with the same ID). Plugin errors
  are in `plugindata` (`pluginError()`), not the top-level `error`. Stock Janus drops connections without
  the `janus-protocol` WebSocket subprotocol.
- **Shared state:** `baseAdapter` serializes WebSocket writes with `wsMu`; the Janus adapter guards its
  transactions with `transMu` and keeps session/handle IDs in `atomic.Int64`.
- **Keyboard** (`internal/controls/keyboard_unix.go`): termios on `/dev/tty` through `x/sys/unix`; don't
  switch to `term.MakeRaw`, which clears `OPOST` (encoders rely on `\n` returning to column 0). `VMIN=0/VTIME=1`
  lets reads return every 100 ms (how `Stop()` ends the loop and a lone Esc is told from arrow keys). `ISIG`
  stays on so Ctrl-C reaches `main` and Ctrl-Z reaches `player` (which pauses the keyboard and the display
  around the stop). Wait with `select`, not `poll` (macOS returns `POLLNVAL` for ttys).
  `keepRaw` re-applies raw mode when another process resets it (ffmpeg does when it exits).
  Terminal code is `//go:build unix`.
- **Codecs:** H.264 only, checked in `validateVideoCodec`.
- **ControlManager:** the keyboard is nil without `-i` (the size monitor still runs), and either can be nil
  if `Start()` failed part way; `processCommands` and `Stop()` cope.
