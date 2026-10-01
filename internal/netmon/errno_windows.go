//go:build windows

package netmon

import "syscall"

// Winsock reports socket failures with WSA* codes, not the POSIX values
// syscall.ECONNREFUSED & co. map to on Windows.
var (
	errnoRefused         = []error{syscall.Errno(10061), syscall.ECONNREFUSED} // WSAECONNREFUSED
	errnoUnreachable     = []error{syscall.Errno(10051), syscall.Errno(10065)} // WSAENETUNREACH, WSAEHOSTUNREACH
	errnoReset           = []error{syscall.Errno(10054), syscall.Errno(10053)} // WSAECONNRESET, WSAECONNABORTED
	errnoPlatformTimeout = []error{syscall.Errno(10060)}                       // WSAETIMEDOUT
)
