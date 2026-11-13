# Decoder test data

`clip.h264`: 20 frames of FFmpeg's `testsrc` pattern, 320×240, H.264 Constrained Baseline
(the profile WebRTC usually negotiates), one slice per frame, Annex-B byte stream, 17.2 KB.
Generated with FFmpeg 9.0.2:

```bash
ffmpeg -f lavfi -i testsrc=size=320x240:rate=15 -frames:v 20 -pix_fmt yuv420p \
  -c:v libx264 -profile:v baseline -tune zerolatency -x264-params sliced-threads=0 -threads 1 -g 15 \
  -bsf:v h264_mp4toannexb -f h264 clip.h264
```

`clip-slices.h264`: the same with three slices per frame (`-x264-params sliced-threads=0:slices=3`), 18.2 KB. Checks that a frame is decoded once, not once per slice.
