package mcp

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	protocol "github.com/mark3labs/mcp-go/mcp"
	"github.com/mcpjungle/mcpjungle/internal/model"
	"github.com/mcpjungle/mcpjungle/pkg/types"
	"gorm.io/gorm"
)

type legacyCatalogWriter struct {
	name   string
	prompt bool
	bulk   bool
}

var legacyCatalogWriters = []legacyCatalogWriter{
	{name: "prompt", prompt: true},
	{name: "server-prompts", prompt: true, bulk: true},
	{name: "resource"},
	{name: "server-resources", bulk: true},
}

func (w legacyCatalogWriter) set(m *MCPService, s *model.McpServer, enabled bool) error {
	entity := s.Name
	if !w.bulk {
		if w.prompt {
			entity = mergeServerPromptNames(s.Name, "catalog")
		} else {
			entity = buildResourceURI(s.Name, "resource://catalog")
		}
	}
	if w.prompt {
		_, err := m.setPromptsEnabled(entity, enabled)
		return err
	}
	_, err := m.setResourcesEnabled(entity, enabled)
	return err
}

func legacyCatalogRows(t *testing.T, db *gorm.DB) *model.McpServer {
	t.Helper()
	s, err := model.NewStdioServer("legacy-catalog", "", "/fixture/backend", nil, nil, types.SessionModeStateless)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Create(s).Error; err != nil {
		t.Fatal(err)
	}
	prompt := &model.Prompt{Name: "catalog", Arguments: []byte("[]"), ServerID: s.ID}
	if err = db.Create(prompt).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Model(prompt).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	resource := &model.Resource{Name: "catalog", URI: buildResourceURI(s.Name, "resource://catalog"), OriginalURI: "resource://catalog", ServerID: s.ID}
	if err = db.Create(resource).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Model(resource).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	return s
}

func assertLegacyCatalogAbsent(t *testing.T, m *MCPService, db *gorm.DB, s *model.McpServer) {
	t.Helper()
	for _, row := range []any{&model.Prompt{}, &model.Resource{}} {
		var count int64
		if err := db.Model(row).Where("server_id = ?", s.ID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("catalog writer resurrected %T after terminal resolve and DELETE: count=%d", row, count)
		}
	}
	if legacyCatalogProxyCount(t, m, true) != 0 || legacyCatalogProxyCount(t, m, false) != 0 {
		t.Fatal("catalog writer published after terminal resolve and DELETE")
	}
}

func legacyCatalogProxyCount(t *testing.T, m *MCPService, prompt bool) int {
	t.Helper()
	method := "resources/list"
	if prompt {
		method = "prompts/list"
	}
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method})
	if err != nil {
		t.Fatal(err)
	}
	response := m.mcpProxyServer.HandleMessage(context.Background(), raw)
	if rpcErr, ok := response.(protocol.JSONRPCError); ok && rpcErr.Error.Code == protocol.METHOD_NOT_FOUND {
		return 0
	}
	result, ok := response.(protocol.JSONRPCResponse)
	if !ok {
		t.Fatalf("unexpected catalog response: %T %v", response, response)
	}
	if prompt {
		list, ok := result.Result.(protocol.ListPromptsResult)
		if !ok {
			t.Fatalf("unexpected prompts result: %T", result.Result)
		}
		return len(list.Prompts)
	}
	list, ok := result.Result.(protocol.ListResourcesResult)
	if !ok {
		t.Fatalf("unexpected resources result: %T", result.Result)
	}
	return len(list.Resources)
}

func TestLegacyCatalogWriterCannotPublishAfterResolveAndDelete(t *testing.T) {
	for _, writer := range legacyCatalogWriters {
		t.Run(writer.name, func(t *testing.T) {
			m, db := registrationService(t)
			s := legacyCatalogRows(t, db)
			paused := make(chan struct{})
			resume := make(chan struct{})
			finished := make(chan struct{})
			result := make(chan error, 1)
			var selected atomic.Bool
			var release sync.Once
			const callback = "fixture:pause-legacy-catalog-writer"
			err := db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
				// Pause after selection, before mutation, without owning the retirement gate.
				if tx.Statement.Schema == nil {
					return
				}
				table := tx.Statement.Schema.Table
				if ((writer.prompt && table == "prompts") || (!writer.prompt && table == "resources")) && selected.CompareAndSwap(false, true) {
					close(paused)
					<-resume
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				release.Do(func() { close(resume) })
				select {
				case <-finished:
				case <-time.After(3 * time.Second):
					t.Error("catalog writer did not finish")
				}
				_ = db.Callback().Query().Remove(callback)
			})
			go func() { result <- writer.set(m, s, true); close(finished) }()
			select {
			case <-paused:
			case <-time.After(3 * time.Second):
				t.Fatal("catalog writer did not select its row")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			input := registrationInputFor(t, s)
			outcome, err := m.ResolveRegistration(ctx, s.Name, input)
			if err != nil || outcome != "present" {
				t.Fatalf("resolve=%s err=%v", outcome, err)
			}
			if err = m.DeregisterMcpServerContext(ctx, s.Name); err != nil {
				t.Fatal(err)
			}
			assertLegacyCatalogAbsent(t, m, db, s)
			release.Do(func() { close(resume) })
			select {
			case <-result:
			case <-time.After(3 * time.Second):
				t.Fatal("catalog writer did not return")
			}
			assertLegacyCatalogAbsent(t, m, db, s)
			outcome, err = m.ResolveRegistration(ctx, s.Name, input)
			if err != nil || outcome != "absent" {
				t.Fatalf("terminal absence changed: resolve=%s err=%v", outcome, err)
			}
		})
	}
}

func TestLegacyCatalogWriterRejectsTerminalPresent(t *testing.T) {
	for _, writer := range legacyCatalogWriters {
		t.Run(writer.name, func(t *testing.T) {
			m, db := registrationService(t)
			s := legacyCatalogRows(t, db)
			outcome, err := m.ResolveRegistration(context.Background(), s.Name, registrationInputFor(t, s))
			if err != nil || outcome != "present" {
				t.Fatalf("resolve=%s err=%v", outcome, err)
			}
			for _, enabled := range []bool{true, false} {
				assertRegistrationConflict(t, writer.set(m, s, enabled), "name_fenced")
			}
			if legacyCatalogProxyCount(t, m, true) != 0 || legacyCatalogProxyCount(t, m, false) != 0 {
				t.Fatal("terminal present accepted catalog publication")
			}
		})
	}
}

func TestLegacyCatalogWriterCompatibility(t *testing.T) {
	for _, runtime := range []string{"unmanaged", "inactive-storage", "active-storage"} {
		t.Run(runtime, func(t *testing.T) {
			for _, writer := range legacyCatalogWriters {
				t.Run(writer.name, func(t *testing.T) {
					m, db := registrationService(t)
					s := legacyCatalogRows(t, db)
					registration := m.registration
					if runtime == "unmanaged" {
						m.registration = nil
					}
					if runtime == "inactive-storage" {
						registration.storage = false
					}
					defer func() { m.registration = registration; registration.storage = true }()
					for _, enabled := range []bool{true, false} {
						if err := writer.set(m, s, enabled); err != nil {
							t.Fatal(err)
						}
						if writer.prompt {
							var row model.Prompt
							if err := db.Where("server_id = ?", s.ID).First(&row).Error; err != nil {
								t.Fatal(err)
							}
							if row.Enabled != enabled || (legacyCatalogProxyCount(t, m, true) == 1) != enabled {
								t.Fatal("prompt toggle changed legacy behavior")
							}
						} else {
							var row model.Resource
							if err := db.Where("server_id = ?", s.ID).First(&row).Error; err != nil {
								t.Fatal(err)
							}
							if row.Enabled != enabled || (legacyCatalogProxyCount(t, m, false) == 1) != enabled {
								t.Fatal("resource toggle changed legacy behavior")
							}
						}
					}
				})
			}
		})
	}
}

func TestManagedToolsOnlyCatalogTogglesRemainEmpty(t *testing.T) {
	m, db := registrationService(t)
	s, err := model.NewStdioServer("tools-only", "", "/fixture/backend", nil, nil, types.SessionModeStateless)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Create(s).Error; err != nil {
		t.Fatal(err)
	}
	definition, err := registrationDefinitionFromServer(s)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.RegistrationLifecycle{Name: s.Name, Definition: definition, ServerID: s.ID, State: "ready"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{true, false} {
		prompts, err := m.setPromptsEnabled(s.Name, enabled)
		if err != nil || len(prompts) != 0 {
			t.Fatalf("prompts=%v err=%v", prompts, err)
		}
		resources, err := m.setResourcesEnabled(s.Name, enabled)
		if err != nil || len(resources) != 0 {
			t.Fatalf("resources=%v err=%v", resources, err)
		}
	}
	assertLegacyCatalogAbsent(t, m, db, s)
}
