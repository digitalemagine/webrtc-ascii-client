package resize

import (
	"image"
	"image/color"
	"testing"
)

func TestAreaAveragesAndKeepsSize(t *testing.T) {
	t.Parallel()

	src := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for x := range 4 {
		v := uint8(0)
		if x >= 2 {
			v = 200
		}
		src.SetRGBA(x, 0, color.RGBA{v, v, v, 255})
		src.SetRGBA(x, 1, color.RGBA{v, v, v, 255})
	}
	if Area(src, 4, 2) != src {
		t.Fatal("same size is not returned unchanged")
	}
	half := Area(src, 2, 1)
	if half.Pix[0] != 0 || half.Pix[4] != 200 || half.Pix[3] != 255 {
		t.Fatalf("2x1 = %v", half.Pix)
	}
	one := Area(src, 1, 1)
	if one.Pix[0] != 100 {
		t.Fatalf("1x1 = %v, want the mean 100", one.Pix)
	}
	big := Area(src, 8, 4) // enlarging: nearest neighbor
	if big.Pix[0] != 0 || big.Pix[4*7] != 200 {
		t.Fatalf("8x4 row 0 = %v", big.Pix[:32])
	}
	odd := Area(src, 3, 3) // non-integer ratios must not panic or leave pixels empty
	for i := 3; i < len(odd.Pix); i += 4 {
		if odd.Pix[i] != 255 {
			t.Fatalf("3x3 pixel %d not written: %v", i/4, odd.Pix)
		}
	}
}

// A sub-image (non-zero bounds) is read from its own rectangle.
func TestAreaSubImage(t *testing.T) {
	t.Parallel()

	src := image.NewRGBA(image.Rect(0, 0, 4, 4))
	src.SetRGBA(2, 2, color.RGBA{9, 9, 9, 255})
	sub := src.SubImage(image.Rect(2, 2, 4, 4)).(*image.RGBA)
	got := Area(sub, 1, 1)
	if got.Pix[0] != 2 { // (9+0+0+0)/4 rounded
		t.Fatalf("got %v", got.Pix)
	}
}

func BenchmarkArea720pTo160x96(b *testing.B) {
	src := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	for b.Loop() {
		Area(src, 160, 96)
	}
}
