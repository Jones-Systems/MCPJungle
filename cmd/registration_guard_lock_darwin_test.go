//go:build darwin

package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mcpjungle/mcpjungle/internal/db"
	"github.com/mcpjungle/mcpjungle/internal/migrations"
	"golang.org/x/sys/unix"
)

func requireRegistrationGuardExcluded(t *testing.T, path string) {
	t.Helper()
	other, err := acquireRegistrationGuard("", path)
	if other != nil {
		_ = other.close()
		t.Fatal("independent open acquired held database gate")
	}
	if !errors.Is(err, unix.EAGAIN) {
		t.Fatalf("expected nonblocking writer exclusion, got %v", err)
	}
}

func TestRegistrationGuardDarwinDescriptionLifetime(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "gateway.db")
	guard, err := acquireRegistrationGuard("", path)
	if err != nil || guard == nil {
		t.Fatalf("guard: %v", err)
	}
	defer guard.close()
	otherPath := filepath.Join(root, "other.db")
	distinct, err := acquireRegistrationGuard("", otherPath)
	if err != nil || distinct == nil {
		t.Fatalf("distinct database blocked: %v", err)
	}
	if err = distinct.close(); err != nil {
		t.Fatal(err)
	}
	for _, alias := range []string{path, filepath.Join(root, "symlink.db"), filepath.Join(root, "hardlink.db")} {
		if alias != path {
			if filepath.Base(alias) == "symlink.db" {
				err = os.Symlink(path, alias)
			} else {
				err = os.Link(path, alias)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if err = guard.verify(alias); err != nil {
			t.Fatal(err)
		}
		requireRegistrationGuardExcluded(t, alias)
	}
	unrelated, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unrelated.Close()
	posix := unix.Flock_t{Start: 1073741824, Len: 1, Type: unix.F_RDLCK, Whence: unix.SEEK_SET}
	if err = unix.FcntlFlock(unrelated.Fd(), unix.F_SETLK, &posix); err != nil {
		t.Fatalf("disjoint SQLite POSIX range blocked: %v", err)
	}
	if err = unrelated.Close(); err != nil {
		t.Fatal(err)
	}
	requireRegistrationGuardExcluded(t, path)
	plain, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = plain.Close(); err != nil {
		t.Fatal(err)
	}
	requireRegistrationGuardExcluded(t, path)
	fd, err := unix.Dup(int(guard.file.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	duplicate := os.NewFile(uintptr(fd), "owned-guard-duplicate")
	defer duplicate.Close()
	if err = guard.close(); err != nil {
		t.Fatal(err)
	}
	requireRegistrationGuardExcluded(t, path)
	if err = duplicate.Close(); err != nil {
		t.Fatal(err)
	}
	replacement, err := acquireRegistrationGuard("", path)
	if err != nil || replacement == nil {
		t.Fatalf("last-description close did not release gate: %v", err)
	}
	defer replacement.close()
}

func TestRegistrationGuardDarwinAcquisitionError(t *testing.T) {
	if err := lockRegistrationGuard(-1); !errors.Is(err, unix.EBADF) {
		t.Fatalf("invalid descriptor did not return acquisition error: %v", err)
	}
	root := t.TempDir()
	if guard, err := acquireRegistrationGuard("", root); err == nil || guard != nil {
		t.Fatalf("directory acquisition falsely claimed ownership: guard=%v err=%v", guard, err)
	}
}

func TestRegistrationGuardDarwinSQLiteMigrationAndCheckpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	guard, err := acquireRegistrationGuard("", path)
	if err != nil || guard == nil {
		t.Fatalf("guard: %v", err)
	}
	defer guard.close()
	conn, err := db.NewDBConnection("", path)
	if err != nil {
		t.Fatal(err)
	}
	sql, err := conn.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sql.Close()
	requireRegistrationGuardExcluded(t, path)
	if err = migrations.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	if !conn.Migrator().HasTable("registration_lifecycles") {
		t.Fatal("registration migration missing")
	}
	requireRegistrationGuardExcluded(t, path)
	if err = conn.Exec("CREATE TABLE guarded_checkpoint_fixture (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	if err = conn.Exec("INSERT INTO guarded_checkpoint_fixture VALUES (1)").Error; err != nil {
		t.Fatal(err)
	}
	var busy, frames, checkpointed int
	if err = conn.Raw("PRAGMA wal_checkpoint(TRUNCATE)").Row().Scan(&busy, &frames, &checkpointed); err != nil || busy != 0 {
		t.Fatalf("guarded checkpoint: busy=%d frames=%d checkpointed=%d err=%v", busy, frames, checkpointed, err)
	}
	var journal string
	var synchronous, count int
	if err = conn.Raw("PRAGMA journal_mode").Scan(&journal).Error; err != nil {
		t.Fatal(err)
	}
	if err = conn.Raw("PRAGMA synchronous").Scan(&synchronous).Error; err != nil {
		t.Fatal(err)
	}
	if err = conn.Raw("SELECT COUNT(*) FROM guarded_checkpoint_fixture").Scan(&count).Error; err != nil || count != 1 || journal != "wal" || synchronous != 2 {
		t.Fatalf("guarded storage: count=%d journal=%s synchronous=%d err=%v", count, journal, synchronous, err)
	}
	if err = sql.Close(); err != nil {
		t.Fatal(err)
	}
	if err = guard.verify(path); err != nil {
		t.Fatal(err)
	}
	requireRegistrationGuardExcluded(t, path)
}
