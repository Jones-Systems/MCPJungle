package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	protocol "github.com/mark3labs/mcp-go/mcp"
	proxy "github.com/mark3labs/mcp-go/server"
	"github.com/mcpjungle/mcpjungle/internal/model"
	"github.com/mcpjungle/mcpjungle/pkg/apierrors"
	"github.com/mcpjungle/mcpjungle/pkg/types"
	"gorm.io/gorm"
)

const RegistrationDeadline = 45 * time.Second
const RegistrationResolveDeadline = 50 * time.Second

// RegistrationConflict is stable management API conflict metadata, without definition contents.
type RegistrationConflict struct{ Code string }

func (e *RegistrationConflict) Error() string { return e.Code }

func conflict(code string) error { return &RegistrationConflict{Code: code} }

type registrationWorker struct {
	cancel          context.CancelFunc
	done            chan struct{}
	definition      string
	cleanupErr      error
	holdsCreateSlot bool
}

type registrationCoordinator struct {
	gate       chan struct{}
	createSlot chan struct{}
	workers    map[string]*registrationWorker
	deleting   map[string]bool
	enabled    bool
	storage    bool
	stopping   bool
}

func newRegistrationCoordinator(c *ServiceConfig) *registrationCoordinator {
	r := &registrationCoordinator{gate: make(chan struct{}, 1), createSlot: make(chan struct{}, 1), workers: map[string]*registrationWorker{}, deleting: map[string]bool{}}
	r.gate <- struct{}{}
	r.createSlot <- struct{}{}
	r.storage = c.DB.Migrator().HasTable(&model.RegistrationLifecycle{})
	r.enabled = r.storage && runtime.GOOS == "linux" && c.DB.Dialector.Name() == "sqlite" && c.RegistrationSingleWriter
	if r.enabled {
		var databases []struct {
			Name string
			File string
		}
		if err := c.DB.Raw("PRAGMA database_list").Scan(&databases).Error; err != nil {
			r.enabled = false
		}
		durable := false
		for _, database := range databases {
			if database.Name == "main" && database.File != "" {
				durable = true
			}
		}
		r.enabled = r.enabled && durable
	}
	return r
}
func (r *registrationCoordinator) lock(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-r.gate:
		if err := ctx.Err(); err != nil {
			r.unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (r *registrationCoordinator) unlock() { r.gate <- struct{}{} }

func (m *MCPService) RegistrationContractAvailable() bool {
	r := m.registration
	if r == nil || !r.enabled {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.lock(ctx); err != nil {
		return false
	}
	defer r.unlock()
	if r.stopping {
		return false
	}
	var count int64
	return m.db.WithContext(ctx).Model(&model.RegistrationLifecycle{}).Count(&count).Error == nil
}

func (m *MCPService) lifecycle(ctx context.Context, name string) (*model.RegistrationLifecycle, error) {
	var row model.RegistrationLifecycle
	err := m.db.WithContext(ctx).Where("name = ?", name).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (m *MCPService) beginRegistration(ctx context.Context, name, definition string, managed bool) (context.Context, *registrationWorker, error) {
	ctx, cancel := context.WithTimeout(ctx, RegistrationDeadline)
	r := m.registration
	if err := r.lock(ctx); err != nil {
		cancel()
		return nil, nil, err
	}
	defer r.unlock()
	if r.stopping {
		cancel()
		return nil, nil, fmt.Errorf("gateway is shutting down")
	}
	if r.deleting[name] {
		cancel()
		return nil, nil, conflict("registration_inflight")
	}
	if managed && !r.enabled {
		cancel()
		return nil, nil, fmt.Errorf("registration contract unavailable: %w", apierrors.ErrInvalidInput)
	}
	if r.storage {
		row, err := m.lifecycle(ctx, name)
		if err != nil {
			cancel()
			return nil, nil, err
		}
		if row != nil {
			cancel()
			if managed && row.Definition != definition {
				return nil, nil, conflict("definition_mismatch")
			}
			if row.Fenced {
				return nil, nil, conflict("name_fenced")
			}
			return nil, nil, conflict("registration_inflight")
		}
	}
	if worker := r.workers[name]; worker != nil {
		cancel()
		if managed && worker.definition != definition {
			return nil, nil, conflict("definition_mismatch")
		}
		return nil, nil, conflict("registration_inflight")
	}
	if managed {
		var count int64
		if err := m.db.WithContext(ctx).Unscoped().Model(&model.McpServer{}).Where("name = ?", name).Count(&count).Error; err != nil {
			cancel()
			return nil, nil, err
		}
		if count != 0 {
			cancel()
			return nil, nil, conflict("definition_mismatch")
		}
		row := model.RegistrationLifecycle{Name: name, Definition: definition, State: "reserved"}
		if err := m.db.WithContext(ctx).Create(&row).Error; err != nil {
			cancel()
			return nil, nil, err
		}
	}
	worker := &registrationWorker{cancel: cancel, done: make(chan struct{}), definition: definition}
	r.workers[name] = worker
	return ctx, worker, nil
}

func (m *MCPService) finishRegistration(name string, w *registrationWorker, managed bool, registrationErr error) {
	w.cancel()
	r := m.registration
	// Worker ownership ends only after client closure and durable failure handling.
	<-r.gate
	defer r.unlock()
	if (managed && registrationErr != nil) || w.cleanupErr != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var persistErr error
		if !managed && w.cleanupErr != nil && r.storage {
			row, err := m.lifecycle(ctx, name)
			if err != nil {
				persistErr = err
			} else if row == nil {
				row = &model.RegistrationLifecycle{Name: name, Definition: w.definition, State: "reserved", Fenced: true, CleanupPending: true}
				var server model.McpServer
				if err = m.db.WithContext(ctx).Where("name = ?", name).First(&server).Error; err == nil {
					row.ServerID = server.ID
				} else if !errors.Is(err, gorm.ErrRecordNotFound) {
					persistErr = err
				}
				if persistErr == nil {
					persistErr = m.db.WithContext(ctx).Create(row).Error
				}
			}
		}
		if persistErr == nil && r.storage {
			persistErr = m.db.WithContext(ctx).Model(&model.RegistrationLifecycle{}).Where("name = ?", name).Updates(map[string]any{"fenced": true, "cleanup_pending": w.cleanupErr != nil}).Error
		}
		if persistErr != nil {
			w.cleanupErr = errors.Join(w.cleanupErr, persistErr)
		}
		cancel()
	}
	if w.cleanupErr == nil {
		delete(r.workers, name)
	}
	if w.holdsCreateSlot {
		r.createSlot <- struct{}{}
		w.holdsCreateSlot = false
	}
	close(w.done)
}

func (m *MCPService) RegisterManagedMcpServer(ctx context.Context, input *types.RegisterServerInput, s *model.McpServer) (resultErr error) {
	definition, err := NormalizeRegistration(input)
	if err != nil {
		return err
	}
	expected, err := model.NewStdioServer(input.Name, input.Description, input.Command, input.Args, input.Env, types.SessionMode(input.SessionMode))
	if err != nil {
		return err
	}
	if s.Name != expected.Name || s.Transport != expected.Transport || s.Description != expected.Description || s.SessionMode != expected.SessionMode || string(s.Config) != string(expected.Config) {
		return conflict("definition_mismatch")
	}
	ctx, worker, err := m.beginRegistration(ctx, s.Name, definition, true)
	if err != nil {
		return err
	}
	defer func() { m.finishRegistration(s.Name, worker, true, resultErr) }()
	if err = m.acquireCreateSlot(ctx, worker); err != nil {
		return err
	}
	c, err := runRegistrationStdioServer(ctx, s, m.mcpServerInitReqTimeoutSec)
	if err != nil {
		var cleanup *ConnectionCleanupError
		if errors.As(err, &cleanup) {
			worker.cleanupErr = cleanup
		}
		return err
	}
	closer, stopClose := closeOnContext(ctx, c)
	defer stopClose()
	var tools []protocol.Tool
	tools, err = discoverManagedTools(ctx, c)
	closeErr := closer.close()
	if closeErr != nil {
		worker.cleanupErr = closeErr
		return fmt.Errorf("temporary client cleanup failed: %w", closeErr)
	}
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	models := make([]model.Tool, 0, len(tools))
	published := make([]proxy.ServerTool, 0, len(tools))
	seenTools := map[string]bool{}
	for _, tool := range tools {
		if seenTools[tool.Name] {
			return fmt.Errorf("duplicate tool name")
		}
		seenTools[tool.Name] = true
		record, err := managedToolModel(tool)
		if err != nil {
			return err
		}
		verified, err := convertToolModelToMcpObject(&record)
		if err != nil {
			return err
		}
		verified.Name = mergeServerToolNames(s.Name, record.Name)
		models = append(models, record)
		published = append(published, proxy.ServerTool{Tool: verified, Handler: m.MCPProxyToolCallHandler})
	}
	r := m.registration
	if err = r.lock(ctx); err != nil {
		return err
	}
	defer r.unlock()
	row, err := m.lifecycle(ctx, s.Name)
	if err != nil {
		return err
	}
	if row == nil || row.Fenced || r.stopping {
		return conflict("name_fenced")
	}
	s.Enabled = true
	if err = m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(s).Error; err != nil {
			return err
		}
		for i := range models {
			models[i].ServerID = s.ID
			if err := tx.Create(&models[i]).Error; err != nil {
				return err
			}
		}
		result := tx.Model(&model.RegistrationLifecycle{}).Where("name = ? AND fenced = ?", s.Name, false).Updates(map[string]any{"server_id": s.ID, "state": "committed", "cleanup_pending": false})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return conflict("name_fenced")
		}
		return nil
	}); err != nil {
		return err
	}
	m.mcpProxyServer.AddTools(published...)
	for _, tool := range published {
		m.addToolInstance(tool.Tool)
	}
	if err = ctx.Err(); err == nil {
		updated := m.db.WithContext(ctx).Model(&model.RegistrationLifecycle{}).Where("name = ? AND fenced = ? AND state = ? AND server_id = ?", s.Name, false, "committed", s.ID).Update("state", "ready")
		err = updated.Error
		if err == nil && updated.RowsAffected != 1 {
			err = conflict("name_fenced")
		}
	}
	if err != nil {
		names := make([]string, len(published))
		for i, t := range published {
			names[i] = t.Tool.Name
		}
		m.mcpProxyServer.DeleteTools(names...)
		m.deleteToolInstances(names...)
		return err
	}
	for _, tool := range published {
		m.notifyToolAddition(tool.Tool.Name)
	}
	m.mcpProxyServer.SendNotificationToAllClients(protocol.MethodNotificationToolsListChanged, nil)
	return ctx.Err()
}

func discoverManagedTools(ctx context.Context, c *client.Client) ([]protocol.Tool, error) {
	caps := c.GetServerCapabilities()
	if caps.Tools == nil || caps.Tasks != nil || caps.Sampling != nil || caps.Elicitation != nil || caps.Roots != nil || caps.Completions != nil {
		return nil, fmt.Errorf("registration contract requires tools-only capabilities: %w", apierrors.ErrInvalidInput)
	}
	if caps.Resources != nil {
		if caps.Resources.Subscribe || caps.Resources.ListChanged {
			return nil, fmt.Errorf("registration contract does not support active resources: %w", apierrors.ErrInvalidInput)
		}
		if err := requireInactiveInventory(ctx, c, "resources/list", "resources"); err != nil {
			return nil, err
		}
		if err := requireInactiveInventory(ctx, c, "resources/templates/list", "resourceTemplates"); err != nil {
			return nil, err
		}
	}
	if caps.Prompts != nil {
		if caps.Prompts.ListChanged {
			return nil, fmt.Errorf("registration contract does not support active prompts: %w", apierrors.ErrInvalidInput)
		}
		if err := requireInactiveInventory(ctx, c, "prompts/list", "prompts"); err != nil {
			return nil, err
		}
	}
	result := []protocol.Tool{}
	cursor := ""
	seen := map[string]bool{}
	for {
		req := protocol.ListToolsRequest{}
		req.Params.Cursor = protocol.Cursor(cursor)
		// Preserve arbitrary JSON schemas through the SDK transport instead of its narrowed schema decoder.
		response, err := c.GetTransport().SendRequest(ctx, transport.JSONRPCRequest{JSONRPC: "2.0", ID: protocol.NewRequestId(fmt.Sprintf("registration-list-%d", len(seen))), Method: "tools/list", Params: req.Params})
		if err != nil {
			return nil, err
		}
		if response == nil {
			return nil, fmt.Errorf("nil tools list")
		}
		if response.Error != nil {
			return nil, fmt.Errorf("tools/list failed: code %d", response.Error.Code)
		}
		var page struct {
			Tools      []json.RawMessage `json:"tools"`
			NextCursor protocol.Cursor   `json:"nextCursor"`
		}
		if err = json.Unmarshal(response.Result, &page); err != nil {
			return nil, err
		}
		if page.Tools == nil {
			return nil, fmt.Errorf("tools list missing")
		}
		for _, data := range page.Tools {
			tool, err := decodeManagedToolDefinition(data)
			if err != nil {
				return nil, err
			}
			result = append(result, tool)
		}
		next := string(page.NextCursor)
		if next == "" {
			return result, nil
		}
		if seen[next] {
			return nil, fmt.Errorf("repeated tools cursor")
		}
		seen[next] = true
		cursor = next
	}
}

func (m *MCPService) ResolveRegistration(ctx context.Context, name string, input *types.RegisterServerInput) (string, error) {
	if input == nil || input.Name != name {
		return "", conflict("definition_mismatch")
	}
	definition, err := NormalizeRegistration(input)
	if err != nil {
		return "", err
	}
	if !m.registration.enabled {
		return "", fmt.Errorf("registration contract unavailable: %w", apierrors.ErrInvalidInput)
	}
	ctx, cancel := context.WithTimeout(ctx, RegistrationResolveDeadline)
	defer cancel()
	w, err := m.fenceRegistration(ctx, name, definition)
	if err != nil {
		return "", err
	}
	if w != nil {
		select {
		case <-w.done:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		if w.cleanupErr != nil {
			return "", fmt.Errorf("registration cleanup pending: %w", w.cleanupErr)
		}
	}
	r := m.registration
	if err = r.lock(ctx); err != nil {
		return "", err
	}
	defer r.unlock()
	row, err := m.lifecycle(ctx, name)
	if err != nil {
		return "", err
	}
	if row == nil || !row.Fenced || row.CleanupPending {
		return "", fmt.Errorf("registration cleanup pending")
	}
	if err = m.closeRegistrationSession(ctx, name); err != nil {
		m.recordClientCleanupFailure(name, err)
		return "", err
	}
	var s model.McpServer
	err = m.db.WithContext(ctx).Where("name = ?", name).First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "absent", ctx.Err()
	}
	if err != nil {
		return "", err
	}
	if row.ServerID != s.ID {
		return "", conflict("definition_mismatch")
	}
	actual, err := registrationDefinitionFromServer(&s)
	if err != nil || actual != definition {
		return "", conflict("definition_mismatch")
	}
	return "present", ctx.Err()
}

func registrationDefinitionFromServer(s *model.McpServer) (string, error) {
	conf, err := s.GetStdioConfig()
	if err != nil {
		return "", err
	}
	return NormalizeRegistration(&types.RegisterServerInput{Name: s.Name, Transport: string(s.Transport), Command: conf.Command, Args: conf.Args, Env: conf.Env, SessionMode: string(s.SessionMode), Description: s.Description})
}

func (m *MCPService) fenceRegistration(ctx context.Context, name, definition string) (*registrationWorker, error) {
	r := m.registration
	if err := r.lock(ctx); err != nil {
		return nil, err
	}
	defer r.unlock()
	row, err := m.lifecycle(ctx, name)
	if err != nil {
		return nil, err
	}
	if row != nil && definition != "" && row.Definition != definition {
		return nil, conflict("definition_mismatch")
	}
	if row != nil && row.ServerID != 0 && definition != "" {
		var current model.McpServer
		if err = m.db.WithContext(ctx).First(&current, row.ServerID).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err == nil {
			actual, definitionErr := registrationDefinitionFromServer(&current)
			if current.Name != name || definitionErr != nil || actual != definition {
				return nil, conflict("definition_mismatch")
			}
		}
	}
	w := r.workers[name]
	if w != nil && definition != "" && w.definition != "" && w.definition != definition {
		return nil, conflict("definition_mismatch")
	}
	if row == nil {
		row = &model.RegistrationLifecycle{Name: name, Definition: definition, State: "reserved", Fenced: true}
		var s model.McpServer
		err = m.db.WithContext(ctx).Where("name = ?", name).First(&s).Error
		if err == nil {
			actual, defErr := registrationDefinitionFromServer(&s)
			if definition != "" && (defErr != nil || actual != definition) {
				return nil, conflict("definition_mismatch")
			}
			row.ServerID = s.ID
			row.State = "ready"
			if definition == "" {
				row.Definition = actual
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err = m.db.WithContext(ctx).Create(row).Error; err != nil {
			return nil, err
		}
	} else if err = m.db.WithContext(ctx).Model(row).Update("fenced", true).Error; err != nil {
		return nil, err
	}
	if w != nil {
		w.cancel()
	}
	// Group catalogs consume deletion callbacks, while global listing also has a readiness filter.
	if row.ServerID != 0 {
		var tools []model.Tool
		if err = m.db.WithContext(ctx).Where("server_id = ?", row.ServerID).Find(&tools).Error; err != nil {
			return nil, err
		}
		names := make([]string, len(tools))
		for i, tool := range tools {
			names[i] = mergeServerToolNames(name, tool.Name)
		}
		m.mcpProxyServer.DeleteTools(names...)
		m.deleteToolInstances(names...)
		m.notifyToolDeletion(names...)
	}
	return w, nil
}

func (m *MCPService) DeregisterMcpServerContext(ctx context.Context, name string) error {
	if err := validateServerName(name); err != nil {
		return err
	}
	if m.registration == nil {
		return m.deregisterLegacyMcpServer(name)
	}
	if !m.registration.storage {
		return m.deregisterLegacyCoordinated(ctx, name)
	}
	row, err := m.lifecycle(ctx, name)
	if err != nil {
		return err
	}
	if row == nil {
		return m.deregisterLegacyCoordinated(ctx, name)
	}
	ctx, cancel := context.WithTimeout(ctx, RegistrationResolveDeadline)
	defer cancel()
	w, err := m.fenceRegistration(ctx, name, "")
	if err != nil {
		return err
	}
	if w != nil {
		select {
		case <-w.done:
		case <-ctx.Done():
			return ctx.Err()
		}
		if w.cleanupErr != nil {
			return w.cleanupErr
		}
	}
	r := m.registration
	if err = r.lock(ctx); err != nil {
		return err
	}
	defer r.unlock()
	row, err = m.lifecycle(ctx, name)
	if err != nil {
		return err
	}
	if row.CleanupPending && row.State != "cleanup_failed" {
		return fmt.Errorf("registration cleanup pending")
	}
	if err = m.closeRegistrationSession(ctx, name); err != nil {
		m.recordClientCleanupFailure(name, err)
		return err
	}
	if err = m.removeManagedRows(ctx, row); err != nil {
		recordCtx, recordCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer recordCancel()
		persistErr := m.db.WithContext(recordCtx).Model(&model.RegistrationLifecycle{}).Where("name = ?", name).Updates(map[string]any{"state": "cleanup_failed", "cleanup_pending": true}).Error
		if persistErr != nil {
			m.retainCleanupFailure(name, errors.Join(err, persistErr))
		}
		return err
	}
	return nil
}

func (m *MCPService) removeManagedRows(ctx context.Context, row *model.RegistrationLifecycle) error {
	var tools []model.Tool
	var prompts []model.Prompt
	var resources []model.Resource
	if row.ServerID != 0 {
		if err := m.db.WithContext(ctx).Unscoped().Where("server_id = ?", row.ServerID).Find(&tools).Error; err != nil {
			return err
		}
		if err := m.db.WithContext(ctx).Unscoped().Where("server_id = ?", row.ServerID).Find(&prompts).Error; err != nil {
			return err
		}
		if err := m.db.WithContext(ctx).Unscoped().Where("server_id = ?", row.ServerID).Find(&resources).Error; err != nil {
			return err
		}
	}
	if err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var named model.McpServer
		namedErr := tx.Unscoped().Where("name = ?", row.Name).First(&named).Error
		if namedErr != nil && !errors.Is(namedErr, gorm.ErrRecordNotFound) {
			return namedErr
		}
		if namedErr == nil && named.ID != row.ServerID {
			return conflict("definition_mismatch")
		}
		if row.ServerID != 0 {
			var stored model.McpServer
			err := tx.Unscoped().First(&stored, row.ServerID).Error
			if err == nil && stored.Name != row.Name {
				return conflict("definition_mismatch")
			}
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			for _, entity := range []any{&model.Tool{}, &model.Prompt{}, &model.Resource{}} {
				if err := tx.Unscoped().Where("server_id = ?", row.ServerID).Delete(entity).Error; err != nil {
					return err
				}
			}
			if err := tx.Unscoped().Where("id = ? AND name = ?", row.ServerID, row.Name).Delete(&model.McpServer{}).Error; err != nil {
				return err
			}
		}
		for _, entity := range []any{&model.UpstreamOAuthToken{}, &model.UpstreamOAuthPendingSession{}} {
			if err := tx.Unscoped().Where("server_name = ?", row.Name).Delete(entity).Error; err != nil {
				return err
			}
		}
		return tx.Model(&model.RegistrationLifecycle{}).Where("name = ?", row.Name).Updates(map[string]any{"server_id": 0, "state": "retired", "fenced": true, "cleanup_pending": false}).Error
	}); err != nil {
		return err
	}
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = mergeServerToolNames(row.Name, t.Name)
	}
	m.mcpProxyServer.DeleteTools(names...)
	m.deleteToolInstances(names...)
	m.notifyToolDeletion(names...)
	promptNames := make([]string, len(prompts))
	for i, p := range prompts {
		promptNames[i] = mergeServerPromptNames(row.Name, p.Name)
	}
	m.mcpProxyServer.DeletePrompts(promptNames...)
	uris := make([]string, len(resources))
	for i, r := range resources {
		uris[i] = r.URI
	}
	m.mcpProxyServer.DeleteResources(uris...)
	return ctx.Err()
}

func (m *MCPService) closeRegistrationSession(ctx context.Context, name string) error {
	sm := m.sessionManager
	for !sm.mu.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	defer sm.mu.Unlock()
	if session := sm.sessions[name]; session != nil {
		if session.Client != nil {
			if err := session.Client.Close(); err != nil {
				return err
			}
		}
		delete(sm.sessions, name)
	}
	return ctx.Err()
}

func (m *MCPService) ShutdownRegistrations(ctx context.Context) error {
	r := m.registration
	if r == nil {
		return nil
	}
	if err := r.lock(ctx); err != nil {
		return err
	}
	r.stopping = true
	workers := make([]*registrationWorker, 0, len(r.workers))
	for _, w := range r.workers {
		w.cancel()
		workers = append(workers, w)
	}
	r.unlock()
	for _, w := range workers {
		select {
		case <-w.done:
		case <-ctx.Done():
			return ctx.Err()
		}
		if w.cleanupErr != nil {
			return w.cleanupErr
		}
	}
	return nil
}

func (m *MCPService) reconcileRegistrations() error {
	if !m.registration.storage {
		return nil
	}
	var rows []model.RegistrationLifecycle
	if err := m.db.Find(&rows).Error; err != nil {
		return err
	}
	for i := range rows {
		row := &rows[i]
		if row.CleanupPending {
			return fmt.Errorf("registration cleanup requires reconciliation for %s", row.Name)
		}
		if row.State == "ready" && !row.Fenced {
			var s model.McpServer
			if err := m.db.First(&s, row.ServerID).Error; err != nil {
				return err
			}
			definition, err := registrationDefinitionFromServer(&s)
			if err != nil || definition != row.Definition {
				return conflict("definition_mismatch")
			}
			continue
		}
		if row.State != "ready" {
			if err := m.removeManagedRows(context.Background(), row); err != nil {
				return err
			}
		}
	}
	// A batch can be installed while committed, but listing and invocation remain closed until ready.
	proxy.WithToolFilter(m.registrationToolFilter)(m.mcpProxyServer)
	return nil
}

func (m *MCPService) withRegistrationPublication(ctx context.Context, name string, publish func() error) error {
	if m.registration == nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		return publish()
	}
	r := m.registration
	if err := r.lock(ctx); err != nil {
		return err
	}
	defer r.unlock()
	if r.stopping {
		return fmt.Errorf("gateway is shutting down")
	}
	if r.deleting[name] {
		return conflict("name_fenced")
	}
	if r.storage {
		row, err := m.lifecycle(ctx, name)
		if err != nil {
			return err
		}
		if row != nil && row.Fenced {
			return conflict("name_fenced")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return publish()
}

// The one gateway-owned create slot covers legacy and negotiated creation, but never resolve/delete.
func (m *MCPService) acquireCreateSlot(ctx context.Context, w *registrationWorker) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.registration.createSlot:
		if err := ctx.Err(); err != nil {
			m.registration.createSlot <- struct{}{}
			return err
		}
		w.holdsCreateSlot = true
		return nil
	}
}

// A failed close cannot be reclassified by an idempotent second Close returning nil.
func (m *MCPService) retainCleanupFailure(name string, err error) {
	w := &registrationWorker{cancel: func() {}, done: make(chan struct{}), cleanupErr: err}
	close(w.done)
	m.registration.workers[name] = w
}
func (m *MCPService) recordClientCleanupFailure(name string, err error) {
	m.retainCleanupFailure(name, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = m.db.WithContext(ctx).Model(&model.RegistrationLifecycle{}).Where("name = ?", name).Update("cleanup_pending", true).Error
}

func (m *MCPService) deregisterLegacyCoordinated(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, RegistrationResolveDeadline)
	defer cancel()
	r := m.registration
	if err := r.lock(ctx); err != nil {
		return err
	}
	if r.deleting[name] {
		r.unlock()
		return conflict("registration_inflight")
	}
	r.deleting[name] = true
	worker := r.workers[name]
	if worker != nil {
		worker.cancel()
	}
	r.unlock()
	defer func() { <-r.gate; delete(r.deleting, name); r.unlock() }()
	if worker != nil {
		select {
		case <-worker.done:
		case <-ctx.Done():
			return ctx.Err()
		}
		if worker.cleanupErr != nil {
			return worker.cleanupErr
		}
	}
	if err := r.lock(ctx); err != nil {
		return err
	}
	defer r.unlock()
	if r.storage {
		row, err := m.lifecycle(ctx, name)
		if err != nil {
			return err
		}
		if row != nil {
			if err = m.db.WithContext(ctx).Model(row).Update("fenced", true).Error; err != nil {
				return err
			}
			if row.CleanupPending {
				return fmt.Errorf("registration cleanup pending")
			}
			return m.removeManagedRows(ctx, row)
		}
	}
	return m.deregisterLegacyMcpServer(name)
}
