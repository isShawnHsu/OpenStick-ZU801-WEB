//go:build !windows

package run

// decodeOutput leaves the bytes untouched. On Linux the runner sets
// LC_ALL=C.UTF-8, so command output is already UTF-8.
func decodeOutput(b []byte) string { return string(b) }
