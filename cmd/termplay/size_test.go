package main

import "testing"

func TestFitSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		mode               string
		frameW, frameH     int
		width, height      int
		termCols, termRows int
		wantW, wantH       int
	}{
		{"both given", "classic", 1280, 720, 100, 30, 0, 0, 100, 30},
		{"width given, 16:9 text", "classic", 1280, 720, 160, 0, 0, 0, 160, 45},
		{"height given, 4:3 text", "braille", 640, 480, 0, 30, 0, 0, 80, 30},
		{"no terminal", "sextant", 1280, 720, 0, 0, 0, 0, 80, 23},
		{"wide terminal: height limits", "classic", 1280, 720, 0, 0, 200, 50, 171, 48},
		{"narrow terminal: width limits", "classic", 640, 480, 0, 0, 60, 50, 60, 23},
		{"image mode in pixels", "iterm2", 1280, 720, 0, 0, 120, 40, 1200, 675},
		{"image width given", "sixel", 640, 480, 320, 0, 0, 0, 320, 240},
		{"image without terminal", "sixel", 1280, 720, 0, 0, 0, 0, 640, 360},
		{"tiny terminal", "classic", 1280, 720, 0, 0, 10, 3, 4, 1},
	}
	for _, tt := range tests {
		w, h := fitSize(tt.mode, tt.frameW, tt.frameH, tt.width, tt.height, tt.termCols, tt.termRows)
		if w != tt.wantW || h != tt.wantH {
			t.Errorf("%s: got %dx%d, want %dx%d", tt.name, w, h, tt.wantW, tt.wantH)
		}
	}
}
