package cliflags

import (
	"flag"
	"strings"
	"testing"
)

func TestPrintDefaultsFoldsAliases(t *testing.T) {
	t.Parallel()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	var render string
	var color bool
	var width int
	for _, n := range []string{"render", "r"} {
		fs.StringVar(&render, n, "classic", "Renderer")
	}
	for _, n := range []string{"color", "c"} {
		fs.BoolVar(&color, n, false, "Color")
	}
	fs.IntVar(&width, "w", 0, "Output `width`")

	var b strings.Builder
	PrintDefaults(&b, fs)
	want := `  -color, -c
    	Color
  -render, -r string
    	Renderer (default "classic")
  -w width
    	Output width
`
	if got := b.String(); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}
