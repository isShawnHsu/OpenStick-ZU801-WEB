//go:build windows

package run

import (
	"syscall"
	"unicode/utf8"
	"unsafe"
)

const cpACP = 0

var procMultiByteToWideChar = syscall.NewLazyDLL("kernel32.dll").NewProc("MultiByteToWideChar")

// decodeOutput converts command output to UTF-8. Windows tools such as ping
// write localized text using the system ANSI code page (for example GBK on a
// Chinese system), which is not valid UTF-8. Without this conversion the JSON
// API would turn those bytes into replacement characters, so the browser shows
// a box full of "garbled" text.
func decodeOutput(b []byte) string {
	if len(b) == 0 || utf8.Valid(b) {
		return string(b)
	}
	// CP_ACP is 0. The last two arguments request the required UTF-16 length.
	n, _, _ := procMultiByteToWideChar.Call(
		uintptr(cpACP), 0,
		uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)),
		0, 0,
	)
	if n == 0 {
		return string(b)
	}
	wide := make([]uint16, int(n))
	procMultiByteToWideChar.Call(
		uintptr(cpACP), 0,
		uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)),
		uintptr(unsafe.Pointer(&wide[0])), n,
	)
	return syscall.UTF16ToString(wide)
}
