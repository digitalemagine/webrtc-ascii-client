package encode_test

import (
	"fmt"
	"os"

	"github.com/digitalemagine/webrtc-ascii-client/encode"
)

// Draw an image as braille characters. The encoder expects an image of
// exactly Config.PixelSize(); scale your image to it first.
func Example() {
	cfg := encode.Config{Mode: encode.Braille, Width: 16, Height: 4}
	enc, err := encode.New(cfg)
	if err != nil {
		fmt.Println(err)
		return
	}
	img := encode.Pattern(cfg.PixelSize()) // any *image.RGBA of that size
	os.Stdout.Write(enc.Encode(nil, img))
	// Output:
	// ⠀⠰⠶⠶⠆⠀⠀⠀⠀⠀⠀⠀⠀⠀⠠⠤
	// ⠀⠀⠀⠀⠀⠀⠀⠀⠀⠠⠤⣀⣐⣬⢭⣽
	// ⠀⠀⣠⠖⢀⣤⢖⣫⣴⣶⣿⣽⣾⣿⣿⣿
	// ⢀⣾⣏⣴⣿⣷⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿
}

// PixelSize tells you what to scale to; with ffmpeg, -vf scale=W:H.
func ExampleConfig_PixelSize() {
	for _, mode := range []string{encode.Classic, encode.Blocks, encode.Braille, encode.Sextant} {
		w, h := encode.Config{Mode: mode, Width: 80, Height: 24}.PixelSize()
		fmt.Printf("%-8s %d:%d\n", mode, w, h)
	}
	// Output:
	// classic  80:24
	// blocks   80:48
	// braille  160:96
	// sextant  160:72
}
