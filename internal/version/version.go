// Package version holds the ic10c release version and build metadata, shared by
// the CLI and the language server.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// Version is the ic10c release version. It can be overridden at build time:
//
//	-ldflags "-X ic10go/internal/version.Version=1.2.3"
var Version = "0.7.0"

// Build metadata. These may be injected with -ldflags; when left empty they
// fall back to the VCS information embedded by the Go toolchain.
var (
	Commit    = ""
	BuildTime = ""
	BuildUser = ""
)

func init() {
	if Commit == "" {
		if rev, ok := vcsSetting("vcs.revision"); ok {
			Commit = short(rev)
		}
	}
	if BuildTime == "" {
		if t, ok := vcsSetting("vcs.time"); ok {
			BuildTime = t
		}
	}
}

func vcsSetting(key string) (string, bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	for _, s := range info.Settings {
		if s.Key == key {
			return s.Value, true
		}
	}
	return "", false
}

func short(hash string) string {
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
}

// Short returns the version, with the commit suffix when known.
func Short() string {
	if Commit != "" {
		return Version + "+" + Commit
	}
	return Version
}

// Details returns a multi-line description of the build.
func Details() string {
	var b strings.Builder
	fmt.Fprintf(&b, "ic10c %s\n", Version)
	if Commit != "" {
		fmt.Fprintf(&b, "  commit:   %s\n", Commit)
	}
	if BuildTime != "" {
		fmt.Fprintf(&b, "  built:    %s\n", BuildTime)
	}
	if BuildUser != "" {
		fmt.Fprintf(&b, "  built by: %s\n", BuildUser)
	}
	fmt.Fprintf(&b, "  go:       %s\n", runtime.Version())
	fmt.Fprintf(&b, "  platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	return b.String()
}
