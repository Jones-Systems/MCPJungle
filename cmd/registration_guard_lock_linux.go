//go:build linux

package cmd

import "syscall"

func lockRegistrationGuard(fd int) error {
	return syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
}
