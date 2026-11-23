// Package buildinfo holds facts about the build that the escalier and
// lsp-server binaries report about themselves.
package buildinfo

// Version is the release version of the escalier and lsp-server binaries.
// Release builds set it with
//
//	go build -ldflags "-X github.com/escalier-lang/escalier/internal/buildinfo.Version=<version>"
//
// Any other build reports "dev".
var Version = "dev"
