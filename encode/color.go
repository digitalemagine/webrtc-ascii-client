package encode

import "math"

// Lookup tables for sRGB ↔ linear conversion and gamma correction.
var (
	srgbToLinearLUT [256]float64
	linearToSrgbLUT [65536]uint8
	gammaLUT        [65536]float64
)

func init() {
	for i := range 256 {
		v := float64(i) / 255.0
		if v <= 0.04045 {
			srgbToLinearLUT[i] = v / 12.92
		} else {
			srgbToLinearLUT[i] = math.Pow((v+0.055)/1.055, 2.4)
		}
	}
	for i := range 65536 {
		v := float64(i) / 65535.0
		if v <= 0.0031308 {
			linearToSrgbLUT[i] = uint8(math.Round(v * 12.92 * 255.0))
		} else {
			linearToSrgbLUT[i] = uint8(math.Round((1.055*math.Pow(v, 1.0/2.4) - 0.055) * 255.0))
		}
		gammaLUT[i] = math.Pow(v, 1.0/2.2)
	}
}

// Rec. 709 luminance coefficients for linear RGB.
const (
	lumR = 0.2126
	lumG = 0.7152
	lumB = 0.0722
)

// linearToSrgb converts a linear value in [0, 1] to sRGB in [0, 1].
func linearToSrgb(v float64) float64 {
	return float64(linearToSrgbLUT[int(clamp01(v)*65535.0)]) / 255.0
}

// gammaCorrect applies a 1/2.2 gamma to a value in [0, 1].
func gammaCorrect(v float64) float64 {
	return gammaLUT[int(clamp01(v)*65535.0)]
}

func floatToUint8(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	return uint8(float64(v*255.0) + 0.5) // explicit rounding: see "Determinism" in encode.go
}

func clamp01(v float64) float64 {
	return min(max(v, 0), 1)
}
