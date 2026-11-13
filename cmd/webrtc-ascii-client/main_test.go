package main

import "testing"

func TestCalculateDimensionsDefaults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name               string
		width, height      int
		ratio, mode        string
		termCols, termRows int
		wantW, wantH       int
	}{
		{"not a terminal", 0, 0, "", "classic", 0, 0, 80, 24},
		{"80x24 terminal leaves the status line free", 0, 0, "", "classic", 80, 24, 80, 22},
		{"large terminal keeps the default", 0, 0, "", "classic", 200, 60, 80, 24},
		{"narrow terminal", 0, 0, "", "sextant", 60, 40, 60, 24},
		{"given size is not reduced", 100, 30, "", "classic", 80, 24, 100, 30},
		{"only the missing dimension is reduced", 100, 0, "", "classic", 80, 24, 100, 22},
		{"ratio computes the height", 0, 0, "16:9", "classic", 80, 24, 80, 22},
		{"ratio taller than the terminal", 0, 0, "4:3", "classic", 80, 24, 58, 22},
		{"ratio, not a terminal", 0, 0, "4:3", "classic", 0, 0, 80, 30},
		{"ratio in a large terminal", 0, 0, "4:3", "classic", 200, 60, 80, 30},
		{"image, not a terminal", 0, 0, "", "sixel", 0, 0, 1280, 720},
		{"image in an 80x24 terminal", 0, 0, "", "iterm2", 80, 24, 800, 440},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w, h, err := calculateDimensions(tt.width, tt.height, tt.ratio, tt.mode, tt.termCols, tt.termRows)
			if err != nil {
				t.Fatal(err)
			}
			if w != tt.wantW || h != tt.wantH {
				t.Errorf("got %dx%d, want %dx%d", w, h, tt.wantW, tt.wantH)
			}
		})
	}
}
