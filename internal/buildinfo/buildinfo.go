// Package buildinfo carries version information injected at build time.
package buildinfo

const (
	// Name is the human readable product name.
	Name = "UZ801 Manager"
	// Slug is used for file names and service descriptions.
	Slug = "uz801d"
)

var (
	// Version is set with -ldflags "-X uz801d/internal/buildinfo.Version=..."
	Version = "dev"
	// Commit is the git revision, if available.
	Commit = "unknown"
	// BuildTime is the UTC build timestamp.
	BuildTime = "unknown"
)
