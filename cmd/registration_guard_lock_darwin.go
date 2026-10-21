//go:build darwin

package cmd

import "golang.org/x/sys/unix"

// macOS SDK 27.0 sys/fcntl.h defines F_OFD_SETLK as 90. The pinned
// x/sys v0.35.0 FcntlFlock wrapper passes its native Flock_t to fcntl.
// Kernels without this operation fail closed on acquisition. Keep the gate outside SQLite's
// POSIX lock range and owned until the last file-description close.
const registrationDarwinOFDSetLK = 90

func lockRegistrationGuard(fd int) error {
	lock := unix.Flock_t{Start: 0, Len: 1, Type: unix.F_WRLCK, Whence: unix.SEEK_SET}
	return unix.FcntlFlock(uintptr(fd), registrationDarwinOFDSetLK, &lock)
}
