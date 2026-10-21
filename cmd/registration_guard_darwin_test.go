//go:build darwin

package cmd

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mcpjungle/mcpjungle/internal/db"
	"gorm.io/gorm"
)

func TestRegistrationGuardNativeWriterFixture(t *testing.T) {
	path := os.Getenv("MCPJUNGLE_GUARD_FIXTURE")
	if path == "" {
		return
	}
	guard, err := acquireRegistrationGuard("", path)
	if err != nil || guard == nil {
		t.Fatalf("fixture guard: %v", err)
	}
	defer guard.close()
	_, _ = io.WriteString(os.Stdout, "held\n")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestRegistrationGuardNativeWriterLifetimeAndWait(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "gateway.db")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestRegistrationGuardNativeWriterFixture$")
	cmd.Dir = root
	cmd.Env = []string{"HOME=" + root, "TMPDIR=" + root, "OTEL_ENABLED=false", "MCPJUNGLE_GUARD_FIXTURE=" + path}
	input, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	// This cleanup is registered after TempDir, so Wait finishes before root removal.
	var once sync.Once
	var waitErr error
	finish := func() {
		once.Do(func() { _ = input.Close(); waitErr = cmd.Wait(); cancel() })
	}
	t.Cleanup(finish)
	scanner := bufio.NewScanner(output)
	if !scanner.Scan() || scanner.Text() != "held" {
		t.Fatal("owned writer did not acquire its guard")
	}
	if other, err := acquireRegistrationGuard("", path); err == nil {
		if other != nil {
			_ = other.close()
		}
		t.Fatal("second writer acquired the child's database inode")
	}
	finish()
	if waitErr != nil || cmd.ProcessState == nil || !cmd.ProcessState.Exited() || !cmd.ProcessState.Success() {
		t.Fatalf("owned writer did not complete Wait: %v", waitErr)
	}
	guard, err := acquireRegistrationGuard("", path)
	if err != nil || guard == nil {
		t.Fatalf("writer guard not released after Wait: %v", err)
	}
	defer guard.close()
}

func TestRegistrationGuardIdentityAndAcquireFailure(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "gateway.db")
	guard, err := acquireRegistrationGuard("", path)
	if err != nil || guard == nil {
		t.Fatalf("guard: %v", err)
	}
	defer guard.close()
	if err = os.Rename(path, filepath.Join(root, "old.db")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if guard.verify(path) == nil {
		t.Fatal("replaced database inode accepted")
	}
	if acquired, err := acquireRegistrationGuard("", filepath.Join(root, "missing", "gateway.db")); err == nil || acquired != nil {
		t.Fatal("acquire failure claimed writer ownership")
	}
}

func TestRegistrationGuardRetainedDescriptorExcludesWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	guard, err := acquireRegistrationGuard("", path)
	if err != nil || guard == nil {
		t.Fatalf("guard: %v", err)
	}
	guard.retain()
	t.Cleanup(func() {
		retainedRegistrationGuards.Lock()
		defer retainedRegistrationGuards.Unlock()
		for i, retained := range retainedRegistrationGuards.guards {
			if retained == guard {
				retainedRegistrationGuards.guards = append(retainedRegistrationGuards.guards[:i], retainedRegistrationGuards.guards[i+1:]...)
				break
			}
		}
		_ = guard.close()
	})
	if other, err := acquireRegistrationGuard("", path); err == nil {
		if other != nil {
			_ = other.close()
		}
		t.Fatal("retained guard released lifetime writer exclusion")
	}
}

func TestRegistrationGuardSQLiteConcurrentDurability(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	guard, err := acquireRegistrationGuard("", path)
	if err != nil || guard == nil {
		t.Fatalf("guard: %v", err)
	}
	defer guard.close()
	first, err := db.NewDBConnection("", path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.NewDBConnection("", path)
	if err != nil {
		t.Fatal(err)
	}
	a, err := first.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := second.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err = first.Exec("CREATE TABLE fixture_writes (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	go func() {
		results <- first.Transaction(func(tx *gorm.DB) error {
			for i := 0; i < 20; i++ {
				if err := tx.Exec("INSERT INTO fixture_writes VALUES (?)", i).Error; err != nil {
					return err
				}
			}
			return nil
		})
	}()
	go func() {
		results <- second.Transaction(func(tx *gorm.DB) error {
			for i := 20; i < 40; i++ {
				if err := tx.Exec("INSERT INTO fixture_writes VALUES (?)", i).Error; err != nil {
					return err
				}
			}
			return nil
		})
	}()
	var writeErr error
	for i := 0; i < 2; i++ {
		writeErr = errors.Join(writeErr, <-results)
	}
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	var count int
	if err = first.Raw("SELECT COUNT(*) FROM fixture_writes").Scan(&count).Error; err != nil || count != 40 {
		t.Fatalf("durable concurrent writes: count=%d err=%v", count, err)
	}
	for _, conn := range []*gorm.DB{first, second} {
		var journal string
		var synchronous int
		if err = conn.Raw("PRAGMA journal_mode").Scan(&journal).Error; err != nil {
			t.Fatal(err)
		}
		if err = conn.Raw("PRAGMA synchronous").Scan(&synchronous).Error; err != nil {
			t.Fatal(err)
		}
		if journal != "wal" || synchronous != 2 {
			t.Fatalf("journal=%s synchronous=%d", journal, synchronous)
		}
	}
}
