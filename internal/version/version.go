// Package version reports what the binary was built from.
//
// Only Version is set with -ldflags -X (the Makefile passes git describe).
// The commit, its time and whether the tree was modified come from the
// build information the Go toolchain embeds (runtime/debug.ReadBuildInfo).
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Version is the version set at link time, e.g. with
// -ldflags "-X github.com/digitalemagine/webrtc-ascii-client/internal/version.Version=v0.1.0".
// When it is empty, Get uses the module version the toolchain stamps (the tag for
// go install …@version, a pseudo-version for go build in a checkout), or "dev".
var Version string

// Info contains all version information
type Info struct {
	Version    string `json:"version"`
	GitCommit  string `json:"git_commit"`
	CommitTime string `json:"commit_time"`
	Modified   bool   `json:"modified"`
	GoVersion  string `json:"go_version"`
	Platform   string `json:"platform"`
}

// Get returns version information
func Get() Info {
	return fromBuildInfo(Version, readBuildInfo())
}

func readBuildInfo() *debug.BuildInfo {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}
	return bi
}

func fromBuildInfo(linkVersion string, bi *debug.BuildInfo) Info {
	info := Info{
		Version:    linkVersion,
		GitCommit:  "unknown",
		CommitTime: "unknown",
		GoVersion:  runtime.Version(),
		Platform:   runtime.GOOS + "/" + runtime.GOARCH,
	}
	if bi != nil {
		if info.Version == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			info.Version = bi.Main.Version
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				info.GitCommit = s.Value
			case "vcs.time":
				info.CommitTime = s.Value
			case "vcs.modified":
				info.Modified = s.Value == "true"
			}
		}
	}
	if info.Version == "" {
		info.Version = "dev"
	}
	return info
}

// Display returns the version as shown to users: a bare release number gets
// a "v" prefix ("0.2.0" -> "v0.2.0"); anything else is shown as is, so a
// git-describe version ("v0.2.0-5-gabc1234"), a commit hash or "dev" doesn't
// get a second or misleading "v".
func (i Info) Display() string {
	if isReleaseNumber(i.Version) {
		return "v" + i.Version
	}
	return i.Version
}

// isReleaseNumber reports whether v starts with "<digits>.<digit>".
func isReleaseNumber(v string) bool {
	digits := 0
	for digits < len(v) && v[digits] >= '0' && v[digits] <= '9' {
		digits++
	}
	return digits > 0 && digits+1 < len(v) && v[digits] == '.' && v[digits+1] >= '0' && v[digits+1] <= '9'
}

// String returns a formatted version string
func (i Info) String() string {
	return "webrtc-ascii-client version " + i.Display()
}

// Detailed returns detailed version information
func (i Info) Detailed() string {
	commit := i.GitCommit
	if i.Modified {
		commit += " (modified)"
	}
	return fmt.Sprintf(`webrtc-ascii-client version %s
Git commit:    %s
Commit time:   %s
Go version:    %s
Platform:      %s`,
		i.Display(),
		commit,
		i.CommitTime,
		i.GoVersion,
		i.Platform,
	)
}
