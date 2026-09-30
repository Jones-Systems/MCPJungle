package mcp

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcpjungle/mcpjungle/internal/model"
	"github.com/mcpjungle/mcpjungle/pkg/types"
	"gorm.io/gorm"
)

func TestManagedStatusWriterCannotResurrectDeletedRegistration(t *testing.T) {
	m, db := registrationService(t)
	input := &types.RegisterServerInput{Name: "status-writer", Transport: "stdio", Command: "/fixture/backend"}
	s, err := model.NewStdioServer(input.Name, "", input.Command, nil, nil, types.SessionModeStateless)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := NormalizeRegistration(input)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Create(s).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.RegistrationLifecycle{Name: s.Name, Definition: definition, ServerID: s.ID, State: "ready"}).Error; err != nil {
		t.Fatal(err)
	}
	paused := make(chan struct{})
	resume := make(chan struct{})
	var release sync.Once
	var selected atomic.Bool
	const callback = "fixture:pause-registration-status-writer"
	if err = db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		row, ok := tx.Statement.Dest.(*model.McpServer)
		if ok && row.Name == s.Name && selected.CompareAndSwap(false, true) {
			close(paused)
			<-resume
		}
	}); err != nil {
		t.Fatal(err)
	}
	writerResult := make(chan error, 1)
	writerFinished := make(chan struct{})
	deleteResult := make(chan error, 1)
	deleteFinished := make(chan struct{})
	deleteStarted := false
	t.Cleanup(func() {
		release.Do(func() { close(resume) })
		select {
		case <-writerFinished:
		case <-time.After(3 * time.Second):
			t.Error("status writer did not finish")
		}
		if deleteStarted {
			select {
			case <-deleteFinished:
			case <-time.After(3 * time.Second):
				t.Error("managed delete did not finish")
			}
		}
		_ = db.Callback().Query().Remove(callback)
	})
	go func() {
		writerResult <- m.setMcpServerEnabled(s.Name, false)
		close(writerFinished)
	}()
	select {
	case <-paused:
	case <-time.After(3 * time.Second):
		t.Fatal("status writer never selected its row")
	}
	deleteStarted = true
	go func() {
		deleteResult <- m.DeregisterMcpServerContext(context.Background(), s.Name)
		close(deleteFinished)
	}()
	deleted := false
	select {
	case err = <-deleteResult:
		deleted = true
		if err != nil {
			t.Fatal(err)
		}
		outcome, err := m.ResolveRegistration(context.Background(), s.Name, input)
		if err != nil || outcome != "absent" {
			t.Fatalf("delete did not produce terminal absence: outcome=%s err=%v", outcome, err)
		}
	case <-time.After(100 * time.Millisecond):
		// A coordinated DELETE waits for the selected writer; release it before awaiting DELETE.
	}
	release.Do(func() { close(resume) })
	if err = <-writerResult; err != nil {
		t.Fatal(err)
	}
	if !deleted {
		if err = <-deleteResult; err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	if err = db.Model(&model.McpServer{}).Where("name = ?", s.Name).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("status writer resurrected a managed row after DELETE: count=%d", count)
	}
	outcome, err := m.ResolveRegistration(context.Background(), s.Name, input)
	if err != nil || outcome != "absent" {
		t.Fatalf("terminal absence changed after status writer: outcome=%s err=%v", outcome, err)
	}
	if err = m.setMcpServerEnabled(s.Name, true); err == nil {
		t.Fatal("fenced absent name accepted status mutation")
	}
}
