//go:build !linux

package svc

import "syscall"

// controlMark is a no-op on platforms without SO_MARK.
func controlMark(network, address string, c syscall.RawConn) error { return nil }
