package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	protocol "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/mcpjungle/mcpjungle/internal/migrations"
	"github.com/mcpjungle/mcpjungle/internal/model"
	"github.com/mcpjungle/mcpjungle/internal/telemetry"
	"github.com/mcpjungle/mcpjungle/pkg/apierrors"
	"github.com/mcpjungle/mcpjungle/pkg/types"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func registrationService(t *testing.T) (*MCPService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "gateway.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sql.SetMaxOpenConns(1)
	if err = migrations.Migrate(db); err != nil {
		t.Fatal(err)
	}
	s := registrationServiceForDB(t, db)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.ShutdownRegistrations(ctx); err != nil {
			t.Errorf("worker drain: %v", err)
		}
		s.sessionManager.Shutdown()
		_ = sql.Close()
	})
	return s, db
}
func registrationServiceForDB(t *testing.T, db *gorm.DB) *MCPService {
	t.Helper()
	s, err := NewMCPService(&ServiceConfig{DB: db, RegistrationSingleWriter: true, McpProxyServer: server.NewMCPServer("fixture", "1", server.WithToolCapabilities(false), server.WithToolFilter(ProxyToolFilter)), SseMcpProxyServer: server.NewMCPServer("sse", "1"), Metrics: telemetry.NewNoopCustomMetrics(), McpServerInitReqTimeout: 2, SessionManager: NewSessionManager(&SessionManagerConfig{DB: db, IdleTimeoutSec: 0, InitReqTimeoutSec: 2})})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func registrationInputFor(t *testing.T, s *model.McpServer) *types.RegisterServerInput {
	t.Helper()
	conf, err := s.GetStdioConfig()
	if err != nil {
		t.Fatal(err)
	}
	return &types.RegisterServerInput{Name: s.Name, Transport: string(s.Transport), Description: s.Description, Command: conf.Command, Args: conf.Args, Env: conf.Env, SessionMode: string(s.SessionMode)}
}
func waitRegistrationFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("fixture did not reach %s", filepath.Base(path))
}
func assertRegistrationConflict(t *testing.T, err error, code string) {
	t.Helper()
	var conflict *RegistrationConflict
	if !errors.As(err, &conflict) || conflict.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}

func TestManagedResolveBeforeCreateAndRetry(t *testing.T) {
	m, _ := registrationService(t)
	s, root := registrationFixture(t, "retired-name", nil)
	input := registrationInputFor(t, s)
	for i := 0; i < 2; i++ {
		outcome, err := m.ResolveRegistration(context.Background(), s.Name, input)
		if err != nil || outcome != "absent" {
			t.Fatalf("resolve=%s err=%v", outcome, err)
		}
	}
	assertRegistrationConflict(t, m.RegisterManagedMcpServer(context.Background(), input, s), "name_fenced")
	if _, err := os.Stat(filepath.Join(root, "pid")); !os.IsNotExist(err) {
		t.Fatal("fenced create launched backend")
	}
	assertRegistrationConflict(t, m.registerMcpServerWithoutOAuth(context.Background(), s), "name_fenced")
	changed := *input
	changed.Command = "/different"
	assertRegistrationConflict(t, func() error { _, err := m.ResolveRegistration(context.Background(), s.Name, &changed); return err }(), "definition_mismatch")
}
func TestManagedResolveCancelsDiscoveryAndStopsPublication(t *testing.T) {
	m, db := registrationService(t)
	s, root := registrationFixture(t, "blocked", map[string]string{"MCPJUNGLE_FIXTURE_BLOCK_LIST": "1"})
	input := registrationInputFor(t, s)
	finished := make(chan error, 1)
	go func() { finished <- m.RegisterManagedMcpServer(context.Background(), input, s) }()
	waitRegistrationFile(t, filepath.Join(root, "listing"))
	assertRegistrationConflict(t, m.RegisterManagedMcpServer(context.Background(), input, s), "registration_inflight")
	outcome, err := m.ResolveRegistration(context.Background(), s.Name, input)
	if err != nil || outcome != "absent" {
		t.Fatalf("resolve=%s err=%v", outcome, err)
	}
	if err = <-finished; err == nil {
		t.Fatal("cancelled registration succeeded")
	}
	_ = os.WriteFile(filepath.Join(root, "release"), []byte("1"), 0600)
	var count int64
	if err = db.Model(&model.McpServer{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("late row count=%d err=%v", count, err)
	}
	tools, err := m.ListTools()
	if err != nil || len(tools) != 0 {
		t.Fatalf("late tools=%v err=%v", tools, err)
	}
}
func TestManagedRequestDeadlineCoversToolsList(t *testing.T) {
	m, _ := registrationService(t)
	s, root := registrationFixture(t, "deadline", map[string]string{"MCPJUNGLE_FIXTURE_BLOCK_LIST": "1"})
	input := registrationInputFor(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := m.RegisterManagedMcpServer(ctx, input, s)
	if err == nil {
		t.Fatal("blocked discovery passed deadline")
	}
	if time.Since(started) > time.Second {
		t.Fatal("discovery did not drain within bounded deadline cleanup")
	}
	if _, err := os.Stat(filepath.Join(root, "listing")); err != nil {
		t.Fatal("fixture never reached tools/list")
	}
	if outcome, err := m.ResolveRegistration(context.Background(), s.Name, input); err != nil || outcome != "absent" {
		t.Fatalf("resolve=%s err=%v", outcome, err)
	}
}
func TestManagedDeleteCancelsDiscoveryAndRetainsFence(t *testing.T) {
	m, _ := registrationService(t)
	s, root := registrationFixture(t, "delete-blocked", map[string]string{"MCPJUNGLE_FIXTURE_BLOCK_LIST": "1"})
	input := registrationInputFor(t, s)
	finished := make(chan error, 1)
	go func() { finished <- m.RegisterManagedMcpServer(context.Background(), input, s) }()
	waitRegistrationFile(t, filepath.Join(root, "listing"))
	if err := m.DeregisterMcpServerContext(context.Background(), s.Name); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err == nil {
		t.Fatal("deleted worker published")
	}
	assertRegistrationConflict(t, m.RegisterManagedMcpServer(context.Background(), input, s), "name_fenced")
}
func TestManagedTransactionRollback(t *testing.T) {
	m, db := registrationService(t)
	s, _ := registrationFixture(t, "rollback", nil)
	input := registrationInputFor(t, s)
	if err := db.Exec("CREATE TRIGGER reject_tool BEFORE INSERT ON tools BEGIN SELECT RAISE(FAIL, 'fixture insert failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	if err := m.RegisterManagedMcpServer(context.Background(), input, s); err == nil {
		t.Fatal("insertion failure accepted")
	}
	var count int64
	if err := db.Model(&model.McpServer{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("partial server rows=%d err=%v", count, err)
	}
	if err := db.Model(&model.Tool{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("partial tool rows=%d err=%v", count, err)
	}
	if m.mcpProxyServer.GetTool("rollback__echo") != nil {
		t.Fatal("failed transaction published catalog")
	}
}
func TestManagedPresentDeleteAndRestartCatalog(t *testing.T) {
	m, db := registrationService(t)
	s, _ := registrationFixture(t, "ready", nil)
	input := registrationInputFor(t, s)
	if err := m.RegisterManagedMcpServer(context.Background(), input, s); err != nil {
		t.Fatal(err)
	}
	before := m.mcpProxyServer.GetTool("ready__echo")
	if before == nil {
		t.Fatal("201 path has no usable proxy tool")
	}
	restarted := registrationServiceForDB(t, db)
	defer restarted.sessionManager.Shutdown()
	after := restarted.mcpProxyServer.GetTool("ready__echo")
	if after == nil {
		t.Fatal("restart lost ready tool")
	}
	a, _ := json.Marshal(before.Tool)
	b, _ := json.Marshal(after.Tool)
	if string(a) != string(b) {
		t.Fatalf("catalog changed on restart")
	}
	outcome, err := restarted.ResolveRegistration(context.Background(), s.Name, input)
	if err != nil || outcome != "present" {
		t.Fatalf("resolve=%s err=%v", outcome, err)
	}
	if tools, err := restarted.ListTools(); err != nil || len(tools) != 0 {
		t.Fatalf("fenced tools remain visible")
	}
	if _, err := restarted.getSession(context.Background(), s); !errors.Is(err, apierrors.ErrNotFound) {
		t.Fatalf("fenced session launch: %v", err)
	}
	if err := restarted.DeregisterMcpServerContext(context.Background(), s.Name); err != nil {
		t.Fatal(err)
	}
	if outcome, err = restarted.ResolveRegistration(context.Background(), s.Name, input); err != nil || outcome != "absent" {
		t.Fatalf("final resolve=%s err=%v", outcome, err)
	}
}
func TestManagedRestartFencesIncompleteRows(t *testing.T) {
	for _, state := range []string{"reserved", "committed"} {
		t.Run(state, func(t *testing.T) {
			_, db := registrationService(t)
			s, _ := registrationFixture(t, "incomplete", nil)
			input := registrationInputFor(t, s)
			definition, _ := NormalizeRegistration(input)
			row := model.RegistrationLifecycle{Name: s.Name, Definition: definition, State: state}
			if state == "committed" {
				if err := db.Create(s).Error; err != nil {
					t.Fatal(err)
				}
				row.ServerID = s.ID
				tool, err := managedToolModel(protocol.NewTool("partial"))
				if err != nil {
					t.Fatal(err)
				}
				tool.ServerID = s.ID
				if err = db.Create(&tool).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			restarted := registrationServiceForDB(t, db)
			defer restarted.sessionManager.Shutdown()
			if tools, err := restarted.ListTools(); err != nil || len(tools) != 0 {
				t.Fatal("restart exposed incomplete tools")
			}
			assertRegistrationConflict(t, restarted.RegisterManagedMcpServer(context.Background(), input, s), "name_fenced")
		})
	}
}
func TestManagedNoTerminalReceiptWithCleanupPending(t *testing.T) {
	m, db := registrationService(t)
	s, _ := registrationFixture(t, "cleanup-pending", nil)
	input := registrationInputFor(t, s)
	definition, _ := NormalizeRegistration(input)
	if err := db.Create(&model.RegistrationLifecycle{Name: s.Name, Definition: definition, State: "reserved", CleanupPending: true}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := m.ResolveRegistration(context.Background(), s.Name, input); err == nil {
		t.Fatal("terminal receipt despite pending cleanup")
	}
	if err := m.DeregisterMcpServerContext(context.Background(), s.Name); err == nil {
		t.Fatal("delete succeeded despite pending cleanup")
	}
}

func TestManagedAndLegacyShareCreateSlotWithCancellableWaiting(t *testing.T) {
	m, _ := registrationService(t)
	ready, _ := registrationFixture(t, "cleanup-ready", nil)
	readyInput := registrationInputFor(t, ready)
	if err := m.RegisterManagedMcpServer(context.Background(), readyInput, ready); err != nil {
		t.Fatal(err)
	}
	legacy, legacyRoot := registrationFixture(t, "old-client", map[string]string{"MCPJUNGLE_FIXTURE_BLOCK_LIST": "1"})
	oldDone := make(chan error, 1)
	go func() { oldDone <- m.registerMcpServerWithoutOAuth(context.Background(), legacy) }()
	waitRegistrationFile(t, filepath.Join(legacyRoot, "listing"))
	managed, managedRoot := registrationFixture(t, "new-client", nil)
	managedInput := registrationInputFor(t, managed)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := m.RegisterManagedMcpServer(ctx, managedInput, managed); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiter error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(managedRoot, "pid")); !os.IsNotExist(err) {
		t.Fatal("waiting new client launched concurrently")
	}
	// Cleanup does not acquire the discovery slot held by the old client.
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
	defer cleanupCancel()
	if err := m.DeregisterMcpServerContext(cleanupCtx, ready.Name); err != nil {
		t.Fatalf("unrelated cleanup waited for create: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyRoot, "release"), []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := <-oldDone; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(managedRoot, "pid")); !os.IsNotExist(err) {
		t.Fatal("cancelled waiter launched after slot release")
	}
}

func TestManagedCompleteToolMetadataSurvivesRestart(t *testing.T) {
	m, db := registrationService(t)
	s, _ := registrationFixture(t, "metadata", nil)
	input := registrationInputFor(t, s)
	definition, _ := NormalizeRegistration(input)
	if err := db.Create(s).Error; err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"name":"rich","description":"full","inputSchema":{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false,"oneOf":[{"properties":{"id":{"const":"first"}}},{"properties":{"id":{"const":"second"}}}]},"outputSchema":{"type":"object","properties":{"ok":{"type":"boolean"}}},"annotations":{"title":"Rich tool","readOnlyHint":true},"_meta":{"vendor":{"hint":"value"}},"icons":[{"src":"https://example.invalid/icon.png"}],"execution":{"taskSupport":"optional"}}`)
	discovered, err := decodeManagedToolDefinition(data)
	if err != nil {
		t.Fatal(err)
	}
	record, err := managedToolModel(discovered)
	if err != nil {
		t.Fatal(err)
	}
	record.ServerID = s.ID
	if err = db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.RegistrationLifecycle{Name: s.Name, Definition: definition, ServerID: s.ID, State: "ready"}).Error; err != nil {
		t.Fatal(err)
	}
	before, err := convertToolModelToMcpObject(&record)
	if err != nil {
		t.Fatal(err)
	}
	before.Name = "metadata__rich"
	restarted := registrationServiceForDB(t, db)
	defer restarted.sessionManager.Shutdown()
	after := restarted.mcpProxyServer.GetTool("metadata__rich")
	if after == nil {
		t.Fatal("restart lost full metadata catalog")
	}
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after.Tool)
	if string(a) != string(b) {
		t.Fatalf("metadata changed: before=%s after=%s", a, b)
	}
	original, _ := json.Marshal(discovered)
	restored := after.Tool
	restored.Name = "rich"
	rest, _ := json.Marshal(restored)
	if string(original) != string(rest) {
		t.Fatal("SDK-supported discovery fields were lost")
	}
	if _, err := m.GetTool("metadata__rich"); err != nil {
		t.Fatal(err)
	}
}

func TestManagedUnusableCatalogNeverAcknowledges(t *testing.T) {
	m, _ := registrationService(t)
	s, _ := registrationFixture(t, "malformed", nil)
	tool := protocol.Tool{Name: "bad", RawInputSchema: json.RawMessage(`{"type":"object"`)}
	if _, err := managedToolModel(tool); err == nil {
		t.Fatal("invalid schema accepted")
	}
	if m.mcpProxyServer.GetTool(s.Name+"__bad") != nil {
		t.Fatal("invalid schema published")
	}
}

func TestManagedFencePublicationSerializationAndTimeout(t *testing.T) {
	m, _ := registrationService(t)
	s, _ := registrationFixture(t, "publishing", nil)
	input := registrationInputFor(t, s)
	callbackStarted := make(chan struct{})
	release := make(chan struct{})
	m.SetToolAdditionCallback(func(string) error { close(callbackStarted); <-release; return nil })
	finished := make(chan error, 1)
	go func() { finished <- m.RegisterManagedMcpServer(context.Background(), input, s) }()
	select {
	case <-callbackStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("publication callback not reached")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, err := m.ResolveRegistration(ctx, s.Name, input)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("resolve issued receipt while publication pending: %v", err)
	}
	close(release)
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
	outcome, err := m.ResolveRegistration(context.Background(), s.Name, input)
	if err != nil || outcome != "present" {
		t.Fatalf("final resolve=%s err=%v", outcome, err)
	}
	if _, err = m.getSession(context.Background(), s); err == nil {
		t.Fatal("terminal fence allowed later launch")
	}
}

func TestManagedDefinitionMismatchDoesNotFenceActiveWorker(t *testing.T) {
	m, db := registrationService(t)
	s, root := registrationFixture(t, "mismatch", map[string]string{"MCPJUNGLE_FIXTURE_BLOCK_LIST": "1"})
	input := registrationInputFor(t, s)
	done := make(chan error, 1)
	go func() { done <- m.RegisterManagedMcpServer(context.Background(), input, s) }()
	waitRegistrationFile(t, filepath.Join(root, "listing"))
	changed := *input
	changed.Description = "different"
	_, err := m.ResolveRegistration(context.Background(), s.Name, &changed)
	assertRegistrationConflict(t, err, "definition_mismatch")
	var row model.RegistrationLifecycle
	if err = db.First(&row, "name = ?", s.Name).Error; err != nil || row.Fenced {
		t.Fatal("mismatch mutated active reservation")
	}
	if err = os.WriteFile(filepath.Join(root, "release"), []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatalf("mismatch cancelled original worker: %v", err)
	}
}

func TestManagedCleanupTransactionFailureRetainsFence(t *testing.T) {
	m, db := registrationService(t)
	s, _ := registrationFixture(t, "delete-failure", nil)
	input := registrationInputFor(t, s)
	if err := m.RegisterManagedMcpServer(context.Background(), input, s); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TRIGGER reject_delete BEFORE DELETE ON tools BEGIN SELECT RAISE(FAIL, 'fixture cleanup failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	if err := m.DeregisterMcpServerContext(context.Background(), s.Name); err == nil {
		t.Fatal("delete succeeded with cleanup pending")
	}
	assertRegistrationConflict(t, m.RegisterManagedMcpServer(context.Background(), input, s), "name_fenced")
	var count int64
	if err := db.Model(&model.McpServer{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatal("cleanup failure partially deleted server")
	}
	if tools, err := m.ListTools(); err != nil || len(tools) != 0 {
		t.Fatal("cleanup failure exposed fenced tools")
	}
	if _, err := m.ResolveRegistration(context.Background(), s.Name, input); err == nil {
		t.Fatal("terminal receipt with failed cleanup")
	}
	if err := db.Exec("DROP TRIGGER reject_delete").Error; err != nil {
		t.Fatal(err)
	}
	if err := m.DeregisterMcpServerContext(context.Background(), s.Name); err != nil {
		t.Fatalf("safe cleanup retry failed: %v", err)
	}
	if outcome, err := m.ResolveRegistration(context.Background(), s.Name, input); err != nil || outcome != "absent" {
		t.Fatalf("cleanup retry outcome=%s err=%v", outcome, err)
	}
}

func TestManagedShutdownCancelsAndDrainsDiscovery(t *testing.T) {
	m, _ := registrationService(t)
	s, root := registrationFixture(t, "shutdown", map[string]string{"MCPJUNGLE_FIXTURE_BLOCK_LIST": "1"})
	input := registrationInputFor(t, s)
	done := make(chan error, 1)
	go func() { done <- m.RegisterManagedMcpServer(context.Background(), input, s) }()
	waitRegistrationFile(t, filepath.Join(root, "listing"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := m.ShutdownRegistrations(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("shutdown acknowledged active registration")
	}
	if m.RegistrationContractAvailable() {
		t.Fatal("shutdown advertised capability")
	}
}

func TestLegacyDeleteDrainsItsWorkerWithoutPermanentRetirement(t *testing.T) {
	m, _ := registrationService(t)
	s, root := registrationFixture(t, "legacy-delete", map[string]string{"MCPJUNGLE_FIXTURE_BLOCK_LIST": "1"})
	done := make(chan error, 1)
	go func() { done <- m.registerMcpServerWithoutOAuth(context.Background(), s) }()
	waitRegistrationFile(t, filepath.Join(root, "listing"))
	if err := m.DeregisterMcpServerContext(context.Background(), s.Name); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("legacy worker published after DELETE")
	}
	if _, err := m.GetMcpServer(s.Name); !errors.Is(err, apierrors.ErrNotFound) {
		t.Fatal("legacy row remains after cleanup")
	}
	if err := os.WriteFile(filepath.Join(root, "release"), []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	replacement, _ := registrationFixture(t, s.Name, nil)
	if err := m.registerMcpServerWithoutOAuth(context.Background(), replacement); err != nil {
		t.Fatalf("ordinary legacy name replacement fenced: %v", err)
	}
}

type registrationFixtureTransport struct {
	capability string
	closeErr   error
	closeCalls int
}

func (f *registrationFixtureTransport) Start(context.Context) error { return nil }
func (f *registrationFixtureTransport) SendNotification(context.Context, protocol.JSONRPCNotification) error {
	return nil
}
func (f *registrationFixtureTransport) SetNotificationHandler(func(protocol.JSONRPCNotification)) {}
func (f *registrationFixtureTransport) GetSessionId() string                                      { return "" }
func (f *registrationFixtureTransport) Close() error {
	f.closeCalls++
	if f.closeCalls == 1 {
		return f.closeErr
	}
	return nil
}
func (f *registrationFixtureTransport) SendRequest(ctx context.Context, req transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
	result := `{"tools":[]}`
	if req.Method == "initialize" {
		result = `{"protocolVersion":"2025-03-26","serverInfo":{"name":"fixture","version":"1"},"capabilities":{"tools":{}` + f.capability + `}}`
	}
	return &transport.JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(result)}, nil
}

func TestManagedRejectsActiveNonToolCapabilities(t *testing.T) {
	for _, field := range []string{"prompts", "resources", "tasks", "sampling", "elicitation", "roots", "completions", "logging", "experimental", "extensions"} {
		t.Run(field, func(t *testing.T) {
			fixture := &registrationFixtureTransport{capability: `,"` + field + `":{}`}
			c := client.NewClient(fixture)
			request := protocol.InitializeRequest{}
			request.Params.ProtocolVersion = protocol.LATEST_PROTOCOL_VERSION
			if _, err := c.Initialize(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			_, err := discoverManagedTools(context.Background(), c)
			passive := field == "logging" || field == "experimental" || field == "extensions"
			if passive && err != nil {
				t.Fatal("passive metadata rejected")
			}
			if !passive && err == nil {
				t.Fatal("active non-tool capability accepted")
			}
		})
	}
}

func TestManagedCloseFailureCannotTurnIntoTerminalReceiptOnRetry(t *testing.T) {
	m, _ := registrationService(t)
	s, _ := registrationFixture(t, "session-close-failure", nil)
	input := registrationInputFor(t, s)
	if err := m.RegisterManagedMcpServer(context.Background(), input, s); err != nil {
		t.Fatal(err)
	}
	fixture := &registrationFixtureTransport{closeErr: errors.New("fixture cleanup still pending")}
	m.sessionManager.sessions[s.Name] = &ManagedSession{ServerName: s.Name, Client: client.NewClient(fixture)}
	// The synthetic transport owns no process or socket; remove only its retained test state at teardown.
	t.Cleanup(func() {
		<-m.registration.gate
		delete(m.registration.workers, s.Name)
		m.registration.unlock()
		m.sessionManager.mu.Lock()
		delete(m.sessionManager.sessions, s.Name)
		m.sessionManager.mu.Unlock()
	})
	for i := 0; i < 2; i++ {
		if _, err := m.ResolveRegistration(context.Background(), s.Name, input); err == nil {
			t.Fatal("cleanup failure produced terminal receipt")
		}
	}
	if fixture.closeCalls != 1 {
		t.Fatal("idempotent second close erased first failure")
	}
}

func TestManagedReadinessPreservesProxyAllowlistFilters(t *testing.T) {
	m, _ := registrationService(t)
	managed, _ := registrationFixture(t, "allowed-managed", nil)
	input := registrationInputFor(t, managed)
	if err := m.RegisterManagedMcpServer(context.Background(), input, managed); err != nil {
		t.Fatal(err)
	}
	legacy, _ := registrationFixture(t, "denied-legacy", nil)
	if err := m.registerMcpServerWithoutOAuth(context.Background(), legacy); err != nil {
		t.Fatal(err)
	}
	list := func(ctx context.Context) []protocol.Tool {
		response := m.mcpProxyServer.HandleMessage(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
		data, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Result struct {
				Tools []protocol.Tool `json:"tools"`
			} `json:"result"`
			Error any `json:"error"`
		}
		if err = json.Unmarshal(data, &result); err != nil || result.Error != nil {
			t.Fatalf("tools/list error: %s", data)
		}
		return result.Result.Tools
	}
	enterprise := context.WithValue(context.Background(), "mode", model.ModeEnterprise)
	enterprise = context.WithValue(enterprise, "client", &model.McpClient{Name: "restricted", AllowList: datatypes.JSON(`["allowed-managed"]`)})
	if tools := list(enterprise); len(tools) != 1 || tools[0].Name != "allowed-managed__echo" {
		t.Fatalf("readiness replaced authorization filter: %v", toolNames(tools))
	}
	missingClient := context.WithValue(context.Background(), "mode", model.ModeEnterprise)
	if tools := list(missingClient); len(tools) != 0 {
		t.Fatal("missing enterprise client exposed tools")
	}
	dev := context.WithValue(context.Background(), "mode", model.ModeDev)
	if tools := list(dev); len(tools) != 2 {
		t.Fatal("development mode lost ready legacy or managed tools")
	}
	if _, err := m.ResolveRegistration(context.Background(), managed.Name, input); err != nil {
		t.Fatal(err)
	}
	if tools := list(enterprise); len(tools) != 0 {
		t.Fatal("fenced managed tool remained visible through composed filters")
	}
	if tools := list(dev); len(tools) != 1 || tools[0].Name != "denied-legacy__echo" {
		t.Fatal("managed retirement changed unrelated legacy catalog")
	}
}

func TestManagedCapabilityDeclinesInMemorySQLite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sql.SetMaxOpenConns(1)
	defer sql.Close()
	if err = migrations.Migrate(db); err != nil {
		t.Fatal(err)
	}
	m := registrationServiceForDB(t, db)
	defer m.sessionManager.Shutdown()
	if m.RegistrationContractAvailable() {
		t.Fatal("in-memory SQLite advertised durable terminal receipts")
	}
}

func TestManagedConflictingStoredDefinitionDoesNotFenceOrDeleteAnotherRow(t *testing.T) {
	m, db := registrationService(t)
	s, _ := registrationFixture(t, "stored-conflict", nil)
	input := registrationInputFor(t, s)
	if err := m.RegisterManagedMcpServer(context.Background(), input, s); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(s).Update("description", "changed outside lifecycle").Error; err != nil {
		t.Fatal(err)
	}
	_, err := m.ResolveRegistration(context.Background(), s.Name, input)
	assertRegistrationConflict(t, err, "definition_mismatch")
	var row model.RegistrationLifecycle
	if err = db.First(&row, "name = ?", s.Name).Error; err != nil || row.Fenced {
		t.Fatal("stored mismatch mutated fence")
	}
	absent, _ := registrationFixture(t, "wrong-row", nil)
	absentInput := registrationInputFor(t, absent)
	if _, err = m.ResolveRegistration(context.Background(), absent.Name, absentInput); err != nil {
		t.Fatal(err)
	}
	if err = db.Create(absent).Error; err != nil {
		t.Fatal(err)
	}
	assertRegistrationConflict(t, m.DeregisterMcpServerContext(context.Background(), absent.Name), "definition_mismatch")
	if _, err = m.GetMcpServer(absent.Name); err != nil {
		t.Fatal("cleanup deleted unbound row")
	}
}
