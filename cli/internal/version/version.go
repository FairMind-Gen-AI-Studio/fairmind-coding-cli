// Package version holds build metadata injected via -ldflags.
package version

var (
	// Version is the CLI semantic version.
	Version = "0.1.0-dev"
	// Commit is the VCS revision the binary was built from.
	Commit = "unknown"
)

// APIContract is the public Agent API contract version the CLI speaks.
const APIContract = "v1"

// ClientID is sent as X-FairMind-Client.
func ClientID() string { return "fairmind-cli/" + Version }
