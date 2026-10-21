package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mcpjungle/mcpjungle/internal/model"
	"github.com/mcpjungle/mcpjungle/pkg/types"
	"gorm.io/gorm"
)

type registrationSessionCleanupTransport struct {
	registrationFixtureTransport
	afterClose func()
}

func (f *registrationSessionCleanupTransport) Close() error {
	err := f.registrationFixtureTransport.Close()
	if f.afterClose != nil {
		f.afterClose()
	}
	return err
}

func registrationSessionCleanupFixture(t *testing.T) (*MCPService, *types.RegisterServerInput, *registrationSessionCleanupTransport) {
	t.Helper()
	m, db := registrationService(t)
	s, err := model.NewStdioServer("session-cleanup", "", "/fixture", nil, nil, types.SessionModeStateful)
	if err != nil {
		t.Fatal(err)
	}
	input := registrationInputFor(t, s)
	definition, err := NormalizeRegistration(input)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.RegistrationLifecycle{Name: s.Name, Definition: definition, State: "reserved"}).Error; err != nil {
		t.Fatal(err)
	}
	f := &registrationSessionCleanupTransport{}
	m.sessionManager.sessions[s.Name] = &ManagedSession{ServerName: s.Name, Client: client.NewClient(f)}
	// This transport owns no external resources; remove retained failure state before service teardown.
	t.Cleanup(func() {
		<-m.registration.gate
		delete(m.registration.workers, s.Name)
		m.registration.unlock()
		m.sessionManager.mu.Lock()
		delete(m.sessionManager.sessions, s.Name)
		m.sessionManager.mu.Unlock()
	})
	return m, input, f
}

func registrationSessionCleanupCall(m *MCPService, ctx context.Context, input *types.RegisterServerInput, operation string) error {
	if operation == "resolve" {
		_, err := m.ResolveRegistration(ctx, input.Name, input)
		return err
	}
	return m.DeregisterMcpServerContext(ctx, input.Name)
}

func TestRegistrationSessionCleanupCancellationAfterCloseCanRetire(t *testing.T) {
	for _, operation := range []string{"resolve", "deregister"} {
		t.Run(operation, func(t *testing.T) {
			m, input, f := registrationSessionCleanupFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.afterClose = cancel
			if err := registrationSessionCleanupCall(m, ctx, input, operation); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled request error = %v", err)
			}
			if f.closeCalls != 1 || m.sessionManager.HasSession(input.Name) {
				t.Fatal("successful close did not remove the session exactly once")
			}
			if m.registration.workers[input.Name] != nil {
				t.Fatal("successful close retained a sticky cleanup failure")
			}
			row, err := m.lifecycle(context.Background(), input.Name)
			if err != nil || row == nil || row.CleanupPending || !row.Fenced {
				t.Fatalf("canceled request lost its retryable fence: row = %+v, error = %v", row, err)
			}
			if outcome, err := m.ResolveRegistration(context.Background(), input.Name, input); err != nil || outcome != "absent" {
				t.Fatalf("fresh resolve retry = %q, error = %v", outcome, err)
			}
			if err := m.DeregisterMcpServerContext(context.Background(), input.Name); err != nil {
				t.Fatalf("cleanup retry failed: %v", err)
			}
			row, err = m.lifecycle(context.Background(), input.Name)
			if err != nil || row == nil || row.State != "retired" || row.CleanupPending {
				t.Fatalf("retry lifecycle = %+v, error = %v", row, err)
			}
			if outcome, err := m.ResolveRegistration(context.Background(), input.Name, input); err != nil || outcome != "absent" {
				t.Fatalf("terminal receipt = %q, error = %v", outcome, err)
			}
			if f.closeCalls != 1 {
				t.Fatal("retry closed the removed session again")
			}
		})
	}
}

func TestRegistrationSessionCleanupCancellationBeforeLockIsNotCloseFailure(t *testing.T) {
	for _, operation := range []string{"resolve", "deregister"} {
		t.Run(operation, func(t *testing.T) {
			m, input, f := registrationSessionCleanupFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			queries := 0
			finalQuery := 2
			if operation == "deregister" {
				finalQuery = 3
			}
			const callback = "fixture:cancel_before_session_lock"
			if err := m.db.Callback().Query().After("gorm:query").Register(callback, func(db *gorm.DB) {
				if db.Statement.Schema != nil && db.Statement.Schema.Name == "RegistrationLifecycle" {
					queries++
					// Cancel after the final lifecycle read, while the session lock is unavailable.
					if queries == finalQuery {
						cancel()
					}
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = m.db.Callback().Query().Remove(callback) })
			m.sessionManager.mu.Lock()
			err := registrationSessionCleanupCall(m, ctx, input, operation)
			m.sessionManager.mu.Unlock()
			if !errors.Is(err, context.Canceled) || queries != finalQuery {
				t.Fatalf("pre-lock cancellation error = %v, lifecycle queries = %d", err, queries)
			}
			if f.closeCalls != 0 || !m.sessionManager.HasSession(input.Name) {
				t.Fatal("pre-lock cancellation closed or removed the session")
			}
			row, err := m.lifecycle(context.Background(), input.Name)
			if err != nil || row == nil || row.CleanupPending || m.registration.workers[input.Name] != nil {
				t.Fatalf("pre-lock cancellation retained cleanup failure: row = %+v, error = %v", row, err)
			}
		})
	}
}

func TestRegistrationSessionCleanupCanceledCloseFailureRemainsSticky(t *testing.T) {
	for _, operation := range []string{"resolve", "deregister"} {
		t.Run(operation, func(t *testing.T) {
			m, input, f := registrationSessionCleanupFixture(t)
			f.closeErr = context.Canceled
			err := registrationSessionCleanupCall(m, context.Background(), input, operation)
			if !errors.Is(err, context.Canceled) || err.Error() != context.Canceled.Error() {
				t.Fatalf("close failure identity or message changed: %v", err)
			}
			if err = registrationSessionCleanupCall(m, context.Background(), input, operation); !errors.Is(err, context.Canceled) {
				t.Fatalf("retry lost the real close failure: %v", err)
			}
			row, err := m.lifecycle(context.Background(), input.Name)
			if err != nil || row == nil || !row.CleanupPending || m.registration.workers[input.Name] == nil {
				t.Fatalf("real close failure lost its barrier: row = %+v, error = %v", row, err)
			}
			if f.closeCalls != 1 || !m.sessionManager.HasSession(input.Name) {
				t.Fatal("retry erased the first close failure through another close")
			}
		})
	}
}

func TestRegistrationSessionCleanupCancellationAfterRetirementIsSuccessful(t *testing.T) {
	m, input, f := registrationSessionCleanupFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.toolDeletionCallback = func(...string) { cancel() }
	if err := m.DeregisterMcpServerContext(ctx, input.Name); err != nil {
		t.Fatalf("committed retirement reported cancellation: %v", err)
	}
	if !errors.Is(ctx.Err(), context.Canceled) || f.closeCalls != 1 {
		t.Fatal("post-commit callback did not cancel after session closure")
	}
	row, err := m.lifecycle(context.Background(), input.Name)
	if err != nil || row == nil || row.State != "retired" || row.CleanupPending {
		t.Fatalf("committed retirement was marked failed: row = %+v, error = %v", row, err)
	}
}

func TestRegistrationSessionCleanupStorageFailureWithCancellationRetainsBarrier(t *testing.T) {
	m, input, _ := registrationSessionCleanupFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	storageErr := errors.New("fixture storage failure")
	const callback = "fixture:storage_failure_with_cancellation"
	if err := m.db.Callback().Query().After("gorm:query").Register(callback, func(db *gorm.DB) {
		if db.Statement.Schema != nil && db.Statement.Schema.Name == "McpServer" {
			cancel()
			db.Error = errors.Join(storageErr, context.Canceled)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.db.Callback().Query().Remove(callback) })
	if err := m.DeregisterMcpServerContext(ctx, input.Name); !errors.Is(err, storageErr) {
		t.Fatalf("genuine storage failure was lost: %v", err)
	}
	row, err := m.lifecycle(context.Background(), input.Name)
	if err != nil || row == nil || row.State != "cleanup_failed" || !row.CleanupPending {
		t.Fatalf("genuine storage failure lost its barrier: row = %+v, error = %v", row, err)
	}
}
