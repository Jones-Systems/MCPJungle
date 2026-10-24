package mcp

import (
	"path/filepath"
	"testing"

	"github.com/mcpjungle/mcpjungle/internal/db"
	"github.com/mcpjungle/mcpjungle/internal/migrations"
)

func TestRegistrationPlatformSupportIsPositive(t *testing.T) {
	for _, platform := range []string{"linux", "darwin", "windows", "freebsd", "", "unknown"} {
		want := platform == "linux" || platform == "darwin"
		if registrationPlatformSupported(platform) != want {
			t.Fatalf("platform %q: supported=%v", platform, !want)
		}
	}
}

func TestRegistrationCapabilityRequiresMigratedGuardedStorage(t *testing.T) {
	conn, err := db.NewDBConnection("", filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	sql, err := conn.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sql.Close()
	config := &ServiceConfig{DB: conn, RegistrationSingleWriter: true}
	unmigrated := &MCPService{db: conn, registration: newRegistrationCoordinator(config)}
	if unmigrated.RegistrationContractAvailable() {
		t.Fatal("unmigrated storage advertised registration contract")
	}
	if err := migrations.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	config.RegistrationSingleWriter = false
	unguarded := &MCPService{db: conn, registration: newRegistrationCoordinator(config)}
	if unguarded.RegistrationContractAvailable() {
		t.Fatal("unguarded storage advertised registration contract")
	}
	config.RegistrationSingleWriter = true
	guarded := &MCPService{db: conn, registration: newRegistrationCoordinator(config)}
	if !guarded.RegistrationContractAvailable() {
		t.Fatal("supported migrated guarded file storage declined registration contract")
	}
}
