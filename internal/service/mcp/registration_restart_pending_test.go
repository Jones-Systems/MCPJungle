package mcp

import (
	"context"
	"fmt"
	protocol "github.com/mark3labs/mcp-go/mcp"
	"reflect"
	"testing"

	"github.com/mcpjungle/mcpjungle/internal/model"
)

func TestRestartPreservesPendingCleanupWithoutBlockingUnrelatedNames(t *testing.T) {
	for _, state := range []string{"reserved", "ready", "cleanup_failed"} {
		t.Run(state, func(t *testing.T) {
			_, db := registrationService(t)
			pending, _ := registrationFixture(t, "pending-old-generation", nil)
			input := registrationInputFor(t, pending)
			definition, err := NormalizeRegistration(input)
			if err != nil {
				t.Fatal(err)
			}
			if err = db.Create(pending).Error; err != nil {
				t.Fatal(err)
			}
			before := model.RegistrationLifecycle{Name: pending.Name, Definition: definition,
				ServerID: pending.ID, State: state, CleanupPending: true}
			if err = db.Create(&before).Error; err != nil {
				t.Fatal(err)
			}
			tool, err := managedToolModel(protocol.NewTool("echo"))
			if err != nil {
				t.Fatal(err)
			}
			tool.ServerID = pending.ID
			if err = db.Create(&tool).Error; err != nil {
				t.Fatal(err)
			}
			restarted := registrationServiceForDB(t, db)
			t.Cleanup(func() { restarted.Shutdown() })
			var after model.RegistrationLifecycle
			if err = db.First(&after, "name = ?", pending.Name).Error; err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("startup changed pending row: %+v, err=%v", after, err)
			}
			status, err := restarted.RegistrationCleanupStatus(context.Background())
			if err != nil || !status.Available || !status.Complete || !reflect.DeepEqual(status.CleanupPendingNames, []string{pending.Name}) {
				t.Fatalf("pending blocker missing: %+v %v", status, err)
			}
			if tools, err := restarted.ListTools(); err != nil || len(tools) != 0 {
				t.Fatalf("pending tools exposed: %+v %v", tools, err)
			}
			published := false
			err = restarted.withRegistrationPublication(context.Background(), pending.Name, func() error { published = true; return nil })
			if err == nil || published {
				t.Fatal("pending catalog publication allowed")
			}
			if err = restarted.registrationReady(context.Background(), pending.Name); err == nil {
				t.Fatal("pending registration became usable")
			}
			if outcome, err := restarted.ResolveRegistration(context.Background(), pending.Name, input); err == nil || outcome != "" {
				t.Fatalf("resolve returned terminal receipt: %q, %v", outcome, err)
			}
			if err = restarted.DeregisterMcpServerContext(context.Background(), pending.Name); err == nil {
				t.Fatal("delete cleared unknown cleanup")
			}
			if err = db.First(&after, "name = ?", pending.Name).Error; err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("operation changed pending row: %+v, err=%v", after, err)
			}
			other, _ := registrationFixture(t, "fresh-independent", nil)
			if err = restarted.RegisterManagedMcpServer(context.Background(), registrationInputFor(t, other), other); err != nil {
				t.Fatalf("unrelated registration blocked: %v", err)
			}
			if err = restarted.DeregisterMcpServerContext(context.Background(), other.Name); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRegistrationCleanupStatusBoundedAndReadOnly(t *testing.T) {
	m, db := registrationService(t)
	rows := make([]model.RegistrationLifecycle, registrationStatusLimit+1)
	for i := range rows {
		rows[i] = model.RegistrationLifecycle{Name: fmt.Sprintf("pending-%03d", i), Definition: "private-definition", State: "reserved", CleanupPending: true}
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	status, err := m.RegistrationCleanupStatus(context.Background())
	if err != nil || !status.Available || status.Complete || len(status.CleanupPendingNames) != registrationStatusLimit {
		t.Fatalf("invalid bounded status: %+v %v", status, err)
	}
	var after []model.RegistrationLifecycle
	if err = db.Order("name").Find(&after).Error; err != nil || !reflect.DeepEqual(rows, after) {
		t.Fatalf("status mutated rows: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = m.RegistrationCleanupStatus(ctx); err == nil {
		t.Fatal("cancelled read succeeded")
	}
}
