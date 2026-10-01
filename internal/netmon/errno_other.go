//go:build !windows

package netmon

import "syscall"

var (
	errnoRefused         = []error{syscall.ECONNREFUSED}
	errnoUnreachable     = []error{syscall.ENETUNREACH, syscall.EHOSTUNREACH}
	errnoReset           = []error{syscall.ECONNRESET, syscall.ECONNABORTED}
	errnoPlatformTimeout = []error{syscall.ETIMEDOUT}
)
