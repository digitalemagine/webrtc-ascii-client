package pipeline

import (
	"time"

	"github.com/digitalemagine/webrtc-ascii-client/encode"
)

const (
	adaptiveCooldown = 3 * time.Second // between quality changes, to avoid oscillation
	adaptiveAlpha    = 0.2             // weight of the newest sample in the moving average
	adaptiveMinFrame = 10              // samples needed before the first decision
)

// qualityController implements -adaptive: it averages encode time and steps
// the enhanced renderer's quality down when encoding exceeds the frame budget
// (1/target FPS), and back up, to at most the starting quality, when it uses
// less than half of it.
type qualityController struct {
	enabled    bool
	budget     time.Duration
	quality    int
	maxQuality int
	avg        time.Duration
	samples    int
	lastChange time.Time
}

func newQualityController(cfg encode.Config, opts Options) *qualityController {
	c := &qualityController{
		enabled:    opts.Adaptive && encode.HasQuality(cfg.Mode) && opts.TargetFPS > 0,
		quality:    cfg.Quality,
		maxQuality: cfg.Quality,
	}
	if c.enabled {
		c.budget = time.Duration(float64(time.Second) / opts.TargetFPS)
	}
	return c
}

// observe records one encode duration and returns the new quality when it
// should change.
func (c *qualityController) observe(d time.Duration, now time.Time) (quality int, change bool) {
	if !c.enabled {
		return c.quality, false
	}
	if c.samples == 0 {
		c.avg, c.lastChange = d, now
	} else {
		c.avg = time.Duration(float64(c.avg)*(1-adaptiveAlpha) + float64(d)*adaptiveAlpha)
	}
	c.samples++
	if c.samples < adaptiveMinFrame || now.Sub(c.lastChange) < adaptiveCooldown {
		return c.quality, false
	}
	switch {
	case c.avg > c.budget && c.quality > 1:
		c.quality--
	case c.avg < c.budget/2 && c.quality < c.maxQuality:
		c.quality++
	default:
		return c.quality, false
	}
	c.lastChange = now
	return c.quality, true
}
