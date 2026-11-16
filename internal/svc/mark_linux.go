//go:build linux

package svc

import "syscall"

// controlMark tags a socket with transparentMark so the transparent proxy rules
// skip it. Used for the "direct uplink" probe.
func controlMark(network, address string, c syscall.RawConn) error {
	var serr error
	if err := c.Control(func(fd uintptr) {
		serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK, transparentMark)
	}); err != nil {
		return err
	}
	return serr
}
