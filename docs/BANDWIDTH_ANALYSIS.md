# Bandwidth Analysis

How many bytes each renderer writes to the terminal per frame.

## What this measures

Every frame is written to the terminal as text and escape sequences. Locally that costs only CPU, but over
SSH, a serial console or a slow PTY the output rate limits the frame rate. The display never queues frames
(it skips to the newest), so a slow link costs frame rate, not delay, but a renderer that writes 2 MB/s at
30 FPS still needs a fast link to look smooth where one writing 60 KB/s doesn't.

The numbers are the bytes one encoder writes for one frame of `encode.Pattern` (gradients, a noise texture
and flat white, black and red rectangles), generated at the renderer's pixel size, as the decoder delivers
frames. The display adds about 200 bytes per frame (cursor control, synchronized update, a two-line status
line on an 80-column terminal). Real video gives different numbers, especially for the image renderers,
whose compression depends on content. KB/s and MB/min assume 30 FPS.

Reproduce with:

```bash
go run ./cmd/bandwidth-analyzer
```

## Results

Measured 2026-10-10 (go1.27.1, darwin/arm64, go-sixel v0.0.14). Text renderer sizes are in characters,
image renderer sizes in pixels. KB = 1,024 bytes, MB = 1,024 KB.

| Renderer | Size | Bytes/frame | KB/s | MB/min |
|---|---|---:|---:|---:|
| Classic | 80×24 | 1,944 | 57 | 3.3 |
| Classic | 160×48 | 7,728 | 226 | 13.3 |
| Enhanced mono, quality 1 | 80×24 | 1,944 | 57 | 3.3 |
| Enhanced mono, quality 4 | 80×24 | 1,944 | 57 | 3.3 |
| Enhanced color, quality 2 | 80×24 | 33,828 | 991 | 58.1 |
| Enhanced color, quality 4 | 80×24 | 34,076 | 998 | 58.5 |
| Blocks mono | 80×24 | 71,055 | 2,082 | 122.0 |
| Blocks color | 80×24 | 70,530 | 2,066 | 121.1 |
| Blocks mono | 160×48 | 283,518 | 8,306 | 486.7 |
| Sextant mono | 80×24 | 7,101 | 208 | 12.2 |
| Sextant color | 80×24 | 69,697 | 2,042 | 119.6 |
| Braille mono | 80×24 | 5,784 | 169 | 9.9 |
| Braille color | 80×24 | 68,679 | 2,012 | 117.9 |
| Sixel | 80×48 px | 13,673 | 401 | 23.5 |
| Sixel | 320×240 px | 122,006 | 3,574 | 209.4 |
| iTerm2 | 80×48 px | 5,970 | 175 | 10.2 |
| iTerm2 | 320×240 px | 86,762 | 2,542 | 148.9 |

## Observations

- **Monochrome text is cheapest.** Classic and enhanced mono write plain ASCII without escape sequences:
  about 2 KB per frame, 57 KB/s at 80×24. Enhanced quality changes what is sampled, not how much is written.
- **Sextant and braille in mono give the most resolution per byte:** 2×3 or 2×4 pixels per cell for 3–4
  times the bytes of classic (about 0.2 MB/s), because each cell is one multi-byte character without color.
- **Color costs 10–17× the same renderer in mono** (sextant 10×, braille 12×, enhanced 17×): each cell gets
  one or two 24-bit color escape sequences of up to 19 bytes each. Enhanced color (one color per cell) is
  the cheapest color mode at about 1 MB/s; sextant, braille and blocks color (two colors per cell) are
  around 2 MB/s.
- **Blocks without color is no cheaper than with color.** Gray levels are written with the same 24-bit
  escape sequences. If bandwidth matters, use sextant or braille mono instead.
- **Output grows with the cell count:** doubling width and height (80×24 → 160×48) multiplies it by about 4.
- **Image renderers depend on pixel size and content,** not on a character grid. The 80×48 px rows have the
  same pixel count as blocks at 80×24 but cover only a few cells on screen; 320×240 px is closer to a usable
  picture and costs about 2.5 MB/s (iTerm2, PNG) to 3.5 MB/s (Sixel) here.

## Recommendations

| Situation | Renderer |
|---|---|
| Slow link, SSH over the internet | `classic`, or `braille`/`sextant` without color for more detail |
| Color over a moderate link | `enhanced -color` |
| Local terminal, best text quality | `sextant -color` or `blocks -color` |
| Local terminal with image support | `iterm2` (iTerm2, WezTerm) or `sixel` |
