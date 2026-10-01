//go:build linux

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

type registrationGuard struct{ file *os.File }

// The lock is on the database inode, so symlink and hard-link aliases share a writer gate.
func acquireRegistrationGuard(dsn, path string) (*registrationGuard, error) {
	if dsn != "" || path == ":memory:" || strings.HasPrefix(path, "file:") || strings.ContainsAny(path, "?#") {
		return nil, nil
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(absolute, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("registration database already has a writer: %w", err)
	}
	return &registrationGuard{file: file}, nil
}
func (g *registrationGuard) verify(path string) error {
	held, err := g.file.Stat()
	if err != nil {
		return err
	}
	active, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(held, active) {
		return fmt.Errorf("registration database identity changed")
	}
	return nil
}
func (g *registrationGuard) close() error { return g.file.Close() }

var retainedRegistrationGuards struct {
	sync.Mutex
	guards []*registrationGuard
}

// Keep the descriptor reachable when a failed drain leaves workers alive.
func (g *registrationGuard) retain() {
	retainedRegistrationGuards.Lock()
	defer retainedRegistrationGuards.Unlock()
	retainedRegistrationGuards.guards = append(retainedRegistrationGuards.guards, g)
}
