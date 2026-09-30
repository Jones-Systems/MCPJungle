//go:build !linux

package cmd

type registrationGuard struct{}

func acquireRegistrationGuard(dsn, path string) (*registrationGuard, error) { return nil, nil }
func (g *registrationGuard) verify(path string) error                       { return nil }
func (g *registrationGuard) close() error                                   { return nil }

func (g *registrationGuard) retain() {}
