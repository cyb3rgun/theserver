// Package version holds build metadata that is injected at link time.
//
// The variables are set with -ldflags, for example:
//
//	go build -ldflags "-X github.com/cyb3rgun/theserver/internal/version.Version=0.1.0-dev" ./cmd/theserver
//
// A binary built without these flags reports "dev" for each of them.
package version

import "runtime"

// Version is the release version of theserver.
var Version = "dev"

// Commit is the git commit the binary was built from.
var Commit = "dev"

// BuildDate is the time the binary was built.
var BuildDate = "dev"

// GoVersion returns the version of the Go toolchain the binary was built with.
func GoVersion() string {
	return runtime.Version()
}
