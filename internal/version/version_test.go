package version

import (
	"runtime/debug"
	"testing"
)

// A version that already starts with "v" doesn't get a second one ("vv0.2.0").
func TestDisplay(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"v0.2.0":                   "v0.2.0",
		"0.2.0":                    "v0.2.0",
		"10.1.3":                   "v10.1.3",
		"v0.2.0-15-g074eda9-dirty": "v0.2.0-15-g074eda9-dirty",
		"dev":                      "dev",
		"3d3d5b4":                  "3d3d5b4", // git describe --always without tags
		"3d3d5b4-dirty":            "3d3d5b4-dirty",
		"1":                        "1",
		"":                         "",
	}
	for in, want := range tests {
		if got := (Info{Version: in}).Display(); got != want {
			t.Errorf("Display(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFromBuildInfo(t *testing.T) {
	t.Parallel()

	vcs := []debug.BuildSetting{
		{Key: "vcs.revision", Value: "abc123"},
		{Key: "vcs.time", Value: "2026-10-06T10:00:00Z"},
		{Key: "vcs.modified", Value: "true"},
	}
	tests := []struct {
		name        string
		linkVersion string
		bi          *debug.BuildInfo
		want        Info
	}{
		{"no build info", "", nil, Info{Version: "dev", GitCommit: "unknown", CommitTime: "unknown"}},
		{"no module version (-buildvcs=false or go run)", "", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: vcs},
			Info{Version: "dev", GitCommit: "abc123", CommitTime: "2026-10-06T10:00:00Z", Modified: true}},
		{"make build", "v0.1.0-3-gabc123", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: vcs},
			Info{Version: "v0.1.0-3-gabc123", GitCommit: "abc123", CommitTime: "2026-10-06T10:00:00Z", Modified: true}},
		{"go install @v0.1.0", "", &debug.BuildInfo{Main: debug.Module{Version: "v0.1.0"}},
			Info{Version: "v0.1.0", GitCommit: "unknown", CommitTime: "unknown"}},
		{"go build in a checkout", "", &debug.BuildInfo{Main: debug.Module{Version: "v0.1.1-0.20261006100000-abc123+dirty"}, Settings: vcs},
			Info{Version: "v0.1.1-0.20261006100000-abc123+dirty", GitCommit: "abc123", CommitTime: "2026-10-06T10:00:00Z", Modified: true}},
	}
	for _, tt := range tests {
		got := fromBuildInfo(tt.linkVersion, tt.bi)
		got.GoVersion, got.Platform = "", ""
		if got != tt.want {
			t.Errorf("%s: got %+v, want %+v", tt.name, got, tt.want)
		}
	}
}
