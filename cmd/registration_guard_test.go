//go:build linux

package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegistrationGuardSameDatabaseInode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	first, err := acquireRegistrationGuard("", path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.close()
	if err = first.verify(path); err != nil {
		t.Fatal(err)
	}
	for _, alias := range []string{path, filepath.Join(filepath.Dir(path), "alias.db"), filepath.Join(filepath.Dir(path), "hard.db")} {
		if alias != path {
			if filepath.Base(alias) == "hard.db" {
				err = os.Link(path, alias)
			} else {
				err = os.Symlink(path, alias)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		second, err := acquireRegistrationGuard("", alias)
		if err == nil {
			_ = second.close()
			t.Fatal("second lifetime writer acquired same database")
		}
	}
	if err = first.close(); err != nil {
		t.Fatal(err)
	}
	replacement, err := acquireRegistrationGuard("", path)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.close()
	if pg, err := acquireRegistrationGuard("postgres://fixture", path); err != nil || pg != nil {
		t.Fatal("Postgres advertised supported guard")
	}
}

func TestRegistrationGuardDoesNotClaimDurabilityForMemoryOrURI(t *testing.T) {
	for _, path := range []string{":memory:", "file:gateway.db?mode=memory", "gateway.db?mode=memory"} {
		guard, err := acquireRegistrationGuard("", path)
		if err != nil || guard != nil {
			t.Fatalf("unsupported path acquired durable guard: %q", path)
		}
	}
}
