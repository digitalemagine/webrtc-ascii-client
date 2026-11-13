# Contributing

This is a first release (0.1) of a small personal project ([how it came about](README.md#about-this-project)).
It does the job it was written for, watching WebRTC cameras over SSH, and not much more. I'd like to know
whether it's useful to anyone else, so an issue that says how you use it, which servers and terminals it
works with, or where it fails is as welcome as code. Fixes and new renderers are welcome too; for anything
bigger, open an issue first so we can talk about it.

## Working with AI assistants

Most of this project was written with an AI assistant, so AI-assisted contributions are welcome. What I ask
is that a person stands behind each one: you wanted the change, you understand it, you ran it, and you're
ready to answer questions about it. Please don't send changes that an agent came up with on its own and
nobody has looked at.

[AGENTS.md](AGENTS.md) holds the notes that coding agents work from: the rules and traps that aren't obvious
from the code. `CLAUDE.md` only imports it, for Claude Code.

## Setup

You need Go 1.27 or newer, a C compiler, `pkg-config` and the FFmpeg development libraries `libavcodec`,
`libavutil` and `libswscale` (H.264 decoding goes through FFmpeg with cgo).

```bash
# macOS (Homebrew)
brew install go ffmpeg pkg-config

# Debian / Ubuntu
sudo apt install build-essential pkg-config libavcodec-dev libavutil-dev libswscale-dev
```

```bash
git clone https://github.com/digitalemagine/webrtc-ascii-client.git
cd webrtc-ascii-client
make check-deps    # pkg-config finds the libraries
make build         # → bin/webrtc-ascii-client, bin/termplay, bin/webrtc-recv, with debug symbols
make demo          # build and run the demo
```

`make release` builds stripped binaries (`-ldflags="-s -w"`, about 35% smaller); `make install` installs
all three into `$GOPATH/bin`. `make help` lists every target.

Only `internal/decoder` links FFmpeg. `termplay`, `webrtc-recv` and the packages they use (`internal/client`,
`internal/h264rtp`, `encode`, …) must build without cgo; CI checks it with
`CGO_ENABLED=0 go test ./cmd/webrtc-recv ./internal/client ./internal/h264rtp`.

## Code map

[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) describes the packages, the path of a frame from RTP to the
terminal, which goroutine owns what, and the signaling sequences. Read it before changing the pipeline or
shutdown. [docs/DEPENDENCY_ANALYSIS.md](docs/DEPENDENCY_ANALYSIS.md) explains each dependency.

## Tests

```bash
make test                     # go test ./...
go test -race ./...           # what CI runs
go test -run TestName ./internal/adapters/...
```

- Timing-dependent code (keepalives, pipeline, display, controls) is tested with `testing/synctest`, not
  sleeps. Use `t.Parallel()` where a test is safe to run concurrently.
- Signaling tests run real pion peers against test servers; `internal/adapters/janustest` is a mock Janus.
  `TestConnectReceivesVideoEndToEnd` in `internal/client` goes from the mock Janus through ICE, DTLS and
  SRTP to H.264 access units on localhost; `cmd/webrtc-recv` checks its output with `ffprobe` when it's
  installed.
- **Golden files.** Encoder output must be byte-identical on every platform, and is checked against
  `encode/testdata/`. After an intended change to an encoder, run `go test ./encode -update` and review
  the diff before committing.
- **Fuzzing.** Everything that parses untrusted input has a fuzz test: `FuzzAssembler` (RTP to access
  units, `internal/h264rtp`), `FuzzH264Stream` (RTP through the decoder, `internal/decoder`) and
  `FuzzReader` (Y4M, `internal/y4m`). CI runs `FuzzH264Stream` for 30 s:
  `go test -run '^$' -fuzz FuzzH264Stream -fuzztime 30s ./internal/decoder`.
- **Untrusted streams.** Video comes from the network, and a broken or hostile sender controls what the
  SPS declares. The decoder refuses frames larger than H.264 allows (Level 6.2, about 8192×4352, through
  FFmpeg's `max_pixels`), so a stream can't make each frame cost gigabytes. Keep limits like this when you
  change the decoder, and add a fuzz test for any new parser.
- **Platforms differ.** FFmpeg runs different SIMD code on amd64 and arm64, so a memory error in the cgo
  code can crash on one and pass on the other. swscale's x86 code, for example, writes past the end of a
  row, which is why `h264.go` pads every image row. A fuzz run that passes on an Apple Silicon Mac proves
  little for amd64; CI on GitHub's amd64 runners is the check. When a fuzz worker dies there, `go test`
  discards its output: a temporary `TestMain` calling `runtime/debug.SetCrashOutput` saves the trace. The
  fuzz test keeps one decoder across inputs, so a saved failing input may pass on its own.

## Public packages

`encode` is the only public package, and its API may still change before 1.0. Everything else is under
`internal/`. If you'd like to use another part from your own code (the signaling sessions, say), open an
issue.

## Lint

```bash
make lint-install  # once: builds the pinned golangci-lint with your Go
make lint
make lint-fix      # apply automatic fixes
```

If `make lint` reports a Go version mismatch, run `make lint-install` again.

## Against real servers

The tests don't need a server. To check a change against real ones, run them in containers and feed them a
test stream with `ffmpeg`. Each command below runs until Ctrl-C: give each its own terminal.

**MediaMTX** (WHEP):

```bash
docker run --rm -p 8554:8554 -p 8889:8889 -p 8189:8189/udp bluenviron/mediamtx
ffmpeg -re -f lavfi -i testsrc=size=320x240:rate=15 -pix_fmt yuv420p -c:v libx264 \
  -profile:v baseline -tune zerolatency -g 15 -f rtsp -rtsp_transport tcp rtsp://localhost:8554/mystream
./bin/webrtc-ascii-client http://localhost:8889/mystream/whep
```

**Janus** (streaming plugin): save this as `janus.plugin.streaming.jcfg`, an RTP mountpoint with ID 1,

```
general: {}
h264-test: {
  type = "rtp"
  id = 1
  description = "H.264 test"
  media = ({
    type = "video"
    mid = "v"
    port = 5004
    pt = 96
    codec = "h264"
    fmtp = "profile-level-id=42e01f;packetization-mode=1"
  })
}
```

start Janus with it (the image's WebSocket transport listens on 8188), then send RTP to port 5004:

```bash
docker run --rm --network host --platform linux/amd64 \
  -v "$PWD/janus.plugin.streaming.jcfg:/usr/local/etc/janus/janus.plugin.streaming.jcfg:ro" \
  canyan/janus-gateway
ffmpeg -re -f lavfi -i testsrc=size=320x240:rate=15 -pix_fmt yuv420p -c:v libx264 \
  -profile:v baseline -tune zerolatency -g 15 \
  -f rtp -payload_type 96 "rtp://127.0.0.1:5004?pkt_size=1200"
./bin/webrtc-ascii-client ws://localhost:8188 1
```

`--network host` lets the client reach the ICE candidates Janus offers; on macOS it needs OrbStack or a
recent Docker Desktop with host networking enabled. The image is amd64 only (about 730 MB) and runs
emulated on Apple silicon.

Expect about 15 FPS in the status line. Watching mountpoint `999` should fail with a clear error, not hang.

Quitting (Ctrl-C, or `q` with `-i`) should leave nothing behind on the server: Janus's
[Admin API](https://janus.conf.meetecho.com/docs/admin.html) `list_sessions` shows one session while playing
and none after, and MediaMTX logs the WebRTC session closing.

## Bandwidth

`go run ./cmd/bandwidth-analyzer` prints bytes per frame for each renderer, the table in
[docs/BANDWIDTH_ANALYSIS.md](docs/BANDWIDTH_ANALYSIS.md). Rerun it when an encoder's output changes.

When the sextant or braille encoder changes, also rerun `internal/tools/rendercompare/run.sh` (needs
chafa, notcurses and tmux) and update [docs/RENDERER_COMPARISON.md](docs/RENDERER_COMPARISON.md) from its
`results.tsv` and pictures.

## Conventions

- Logging goes through `log/slog`. stdout carries video frames only: never `fmt.Print*` or `log.Printf` in
  library code. Warnings and errors appear in the status line; Info and Debug only with `-V`.
- One goroutine owns each resource (decoder, encoder, stdout); hand frames over through `latest.Slot`
  rather than sharing them. See the ownership section of the architecture document.
- Return errors to `main` rather than calling `os.Exit`, so the terminal is always restored.
- American English in code, comments and docs ("color").
- Commit messages: `area: what changed`, lower case, e.g. `decoder: scale in sws_scale`, or
  `docs: …` for documentation. Add an entry under `[Unreleased]` in [CHANGELOG.md](CHANGELOG.md) for
  anything a user would notice.

## Releases

The project follows [Semantic Versioning](https://semver.org/). `make build`, `make release` and
`make install` embed `git describe --tags --always --dirty` as the version; otherwise the Go toolchain's
module version is used (the tag for `go install …@v0.1.0`, a pseudo-version for `go build` in a checkout,
`dev` without either).

To release:

1. Move the `[Unreleased]` entries in `CHANGELOG.md` under the new version and date.
2. Tag and push: `git tag -a v0.1.0 -m "v0.1.0" && git push origin v0.1.0`.

## License

By contributing you agree that your contributions are licensed under the [MIT License](LICENSE).
