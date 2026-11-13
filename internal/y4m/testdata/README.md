# Y4M test data

Two 32×24 frames of FFmpeg's `testsrc` per pixel format, and FFmpeg's own RGBA conversion of the same
frames as the reference (`.rgba`, raw, 32×24×4 bytes per frame). Generated with FFmpeg 9.0.2:

```bash
for pf in yuv420p yuvj420p yuv422p yuv444p gray; do
  ffmpeg -f lavfi -i testsrc=size=32x24:rate=10 -frames:v 2 -pix_fmt $pf -strict -1 -f yuv4mpegpipe $pf.y4m
  ffmpeg -i $pf.y4m -f rawvideo -pix_fmt rgba $pf.rgba
done
```

`yuvj420p` and `gray` are full range (`XCOLORRANGE=FULL`); the others are limited range.
