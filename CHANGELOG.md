# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-11-14

First release.

### Added
- `webrtc-ascii-client`: plays the H.264 video of a WebRTC stream in the
  terminal. Signaling for the Janus streaming plugin, WHEP (e.g. MediaMTX) and
  a small JSON-over-WebSocket protocol of its own (`docs/SIGNALING.md`); the
  adapter is chosen from the URL, or with `-adapter`. Decoding and scaling use
  the FFmpeg libraries through cgo
- Seven renderers: `classic` and `enhanced` ASCII, `blocks` (half blocks),
  `braille`, `sextant`, and real images with `sixel` and `iterm2`; 24-bit
  color for `enhanced`, `blocks`, `braille` and `sextant`
- Display on the alternate screen with synchronized updates; frames are dropped
  rather than queued when the terminal is slower than the stream, and the
  terminal is restored on exit and while suspended with Ctrl-Z. A status
  line shows the renderer, frame rate, dropped frames and warnings
- Sizing with `-width`, `-height` and `-ratio`; the default is 80×24
  characters, less in a smaller terminal. When the terminal is resized, the
  picture shrinks to fit and grows back to the size asked for, with or
  without `-i`
- Keyboard controls with `-i`: switch renderer, color, quality, cycle sizes,
  help, quit
- `-adaptive` lowers the `enhanced` quality when encoding can't keep up with
  `-target-fps`
- Out-of-order RTP packets are reordered; after packet loss, and when a track
  starts, the client asks the sender for a keyframe (RTCP PLI)
- `-stun` sets the STUN servers (`-stun ""` for none)
- `-demo` plays a test pattern without a server
- Logging with `log/slog` on stderr (`-V 1` progress, `-V 2` debug); stdout
  carries only the video
- `termplay`: plays YUV4MPEG2 video (e.g. from `ffmpeg -f yuv4mpegpipe`) with the
  same renderers; pure Go
- `webrtc-recv`: receives a WebRTC video track and writes it as H.264 Annex-B to
  stdout or a file, for ffmpeg, ffplay or a recording; pure Go
- `encode`: the renderers as a public Go package (image in, terminal bytes out)
- `bandwidth-analyzer` and `docs/BANDWIDTH_ANALYSIS.md`: output size of each
  renderer, measured
- Documentation: README, `docs/USAGE.md`, `docs/SIGNALING.md`,
  `docs/ARCHITECTURE.md`, `docs/RENDERER_COMPARISON.md`, `CONTRIBUTING.md`
- GitHub Actions CI: vet, race tests, lint, fuzz smoke test, govulncheck,
  Markdown link check
- MIT license

[Unreleased]: https://github.com/digitalemagine/webrtc-ascii-client/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/digitalemagine/webrtc-ascii-client/releases/tag/v0.1.0
