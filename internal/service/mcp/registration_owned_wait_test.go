//go:build linux || darwin

package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	protocol "github.com/mark3labs/mcp-go/mcp"
	"github.com/mcpjungle/mcpjungle/internal/model"
	"github.com/mcpjungle/mcpjungle/pkg/types"
)

func TestTransportOwnedRegistrationStdioFixture(t *testing.T) {
	if os.Getenv("MCPJUNGLE_OWNED_FIXTURE") != "1" {
		return
	}
	root := os.Getenv("MCPJUNGLE_OWNED_ROOT")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil || len(request.ID) == 0 {
			continue
		}
		var result any
		switch request.Method {
		case "initialize":
			if os.Getenv("MCPJUNGLE_OWNED_NO_INIT") == "1" {
				_ = os.WriteFile(filepath.Join(root, "initializing"), nil, 0600)
				// Request-lifetime cancellation must own termination, even when close races stdin EOF.
				for os.Getenv("MCPJUNGLE_OWNED_HOLD_INIT") == "1" {
					time.Sleep(time.Hour)
				}
				continue
			}
			result = map[string]any{"protocolVersion": "2025-03-26", "serverInfo": map[string]string{"name": "transport-fixture", "version": "1"}, "capabilities": map[string]any{"tools": map[string]any{}}}
		case "tools/call":
			_ = os.WriteFile(filepath.Join(root, "calling"), nil, 0600)
			if os.Getenv("MCPJUNGLE_OWNED_BLOCK_CALL") == "1" {
				continue
			}
			result = map[string]any{"content": []any{}}
		case "tools/list":
			_ = os.WriteFile(filepath.Join(root, "listing"), nil, 0600)
			if os.Getenv("MCPJUNGLE_OWNED_BLOCK_LIST") == "1" {
				for {
					if _, err := os.Stat(filepath.Join(root, "release")); err == nil {
						break
					}
					time.Sleep(5 * time.Millisecond)
				}
			}
			result = map[string]any{"tools": []any{map[string]any{"name": "echo", "inputSchema": map[string]any{"type": "object"}}}}
		default:
			result = map[string]any{}
		}
		data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		fmt.Println(string(data))
	}
	os.Exit(0)
}

// Serial top-level tests restore this factory before Go releases parallel tests.
// Service cleanup is registered later and runs before this harness checks Wait and removes its root.
func transportOwnedRegistrationHarness(t *testing.T) (string, func() []*exec.Cmd) {
	t.Helper()
	root, err := os.MkdirTemp("", "mcpjungle-transport-owned-")
	if err != nil {
		t.Fatal(err)
	}
	original := registrationCommandContext
	originalLongLived := registrationCommand
	var mu sync.Mutex
	var commands []*exec.Cmd
	registrationCommandContext = func(ctx context.Context, command string, args ...string) *exec.Cmd {
		cmd := original(ctx, command, args...)
		cmd.Dir = root
		mu.Lock()
		commands = append(commands, cmd)
		mu.Unlock()
		return cmd
	}
	registrationCommand = func(command string, args ...string) *exec.Cmd {
		cmd := originalLongLived(command, args...)
		cmd.Dir = root
		mu.Lock()
		commands = append(commands, cmd)
		mu.Unlock()
		return cmd
	}
	snapshot := func() []*exec.Cmd {
		mu.Lock()
		defer mu.Unlock()
		return append([]*exec.Cmd(nil), commands...)
	}
	t.Cleanup(func() {
		defer func() { registrationCommandContext = original; registrationCommand = originalLongLived }()
		if t.Failed() {
			t.Logf("failed qualification; wait_unknown commands and fixture root retained: %s", root)
			return
		}
		for _, cmd := range snapshot() {
			if cmd.Process != nil && cmd.ProcessState == nil {
				t.Errorf("transport Wait not proved; fixture root retained: %s", root)
				return
			}
		}
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("fixture cleanup: %v", err)
		}
	})
	return root, snapshot
}

func transportOwnedRegistrationFixture(t *testing.T, root, name string, extra map[string]string) *model.McpServer {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": root, "TMPDIR": root, "OTEL_ENABLED": "false", "MCPJUNGLE_OWNED_FIXTURE": "1", "MCPJUNGLE_OWNED_ROOT": root}
	for key, value := range extra {
		env[key] = value
	}
	s, err := model.NewStdioServer(name, "", executable, []string{"-test.run=^TestTransportOwnedRegistrationStdioFixture$"}, env, types.SessionModeStateless)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func assertOwnedTransportWait(t *testing.T, commands []*exec.Cmd) {
	t.Helper()
	if len(commands) == 0 {
		t.Fatal("fixture did not construct an owned command")
	}
	for _, cmd := range commands {
		if cmd.Process == nil || cmd.ProcessState == nil {
			t.Fatal("transport-owned Wait did not produce ProcessState")
		}
		status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
		if !ok || (!status.Exited() && !status.Signaled()) {
			t.Fatalf("unclassified Wait result: %v", cmd.ProcessState)
		}
		t.Logf("owned transport Wait: exited=%v signaled=%v code=%d", status.Exited(), status.Signaled(), cmd.ProcessState.ExitCode())
	}
}

func TestTransportOwnedRegistrationInitializeCancellationReaps(t *testing.T) {
	root, commands := transportOwnedRegistrationHarness(t)
	m, _ := registrationService(t)
	s := transportOwnedRegistrationFixture(t, root, "native-no-init", map[string]string{"MCPJUNGLE_OWNED_NO_INIT": "1", "MCPJUNGLE_OWNED_HOLD_INIT": "1"})
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := m.RegisterManagedMcpServer(ctx, registrationInputFor(t, s), s); err == nil {
		t.Fatal("initialization cancellation acknowledged success")
	}
	if m.registration.workers[s.Name] != nil {
		t.Fatal("cleanup remains fenced; transport Wait is wait_unknown")
	}
	assertOwnedTransportWait(t, commands())
}

func TestTransportOwnedRegistrationDiscoveryCancellationReapsAndFences(t *testing.T) {
	root, commands := transportOwnedRegistrationHarness(t)
	m, _ := registrationService(t)
	s := transportOwnedRegistrationFixture(t, root, "native-discovery", map[string]string{"MCPJUNGLE_OWNED_BLOCK_LIST": "1"})
	input := registrationInputFor(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := m.RegisterManagedMcpServer(ctx, input, s); err == nil {
		t.Fatal("discovery cancellation acknowledged success")
	}
	if m.registration.workers[s.Name] != nil {
		t.Fatal("cleanup remains fenced; transport Wait is wait_unknown")
	}
	if _, err := os.Stat(filepath.Join(root, "listing")); err != nil {
		t.Fatal("fixture never reached discovery")
	}
	assertOwnedTransportWait(t, commands())
	if tools, err := m.ListTools(); err != nil || len(tools) != 0 {
		t.Fatal("cancelled discovery published tools")
	}
	outcome, err := m.ResolveRegistration(context.Background(), s.Name, input)
	if err != nil || outcome != "absent" {
		t.Fatalf("resolve=%s err=%v", outcome, err)
	}
	assertRegistrationConflict(t, m.RegisterManagedMcpServer(context.Background(), input, s), "name_fenced")
	if len(commands()) != 1 {
		t.Fatal("fenced retry launched another child")
	}
}

func TestTransportOwnedRegistrationRestartPreservesCatalogAndReaping(t *testing.T) {
	root, commands := transportOwnedRegistrationHarness(t)
	m, db := registrationService(t)
	s := transportOwnedRegistrationFixture(t, root, "native-restart", nil)
	input := registrationInputFor(t, s)
	if err := m.RegisterManagedMcpServer(context.Background(), input, s); err != nil {
		t.Fatal(err)
	}
	assertOwnedTransportWait(t, commands())
	restarted := registrationServiceForDB(t, db)
	defer restarted.sessionManager.Shutdown()
	if restarted.mcpProxyServer.GetTool(s.Name+"__echo") == nil {
		t.Fatal("restart lost catalog")
	}
	outcome, err := restarted.ResolveRegistration(context.Background(), s.Name, input)
	if err != nil || outcome != "present" {
		t.Fatalf("resolve=%s err=%v", outcome, err)
	}
	if err := restarted.DeregisterMcpServerContext(context.Background(), s.Name); err != nil {
		t.Fatal(err)
	}
	if len(commands()) != 1 {
		t.Fatal("restart or terminal cleanup started another child")
	}
	assertOwnedTransportWait(t, commands())
}

func TestTransportOwnedRegistrationFailedDrainDeclinesCapability(t *testing.T) {
	_, commands := transportOwnedRegistrationHarness(t)
	m, _ := registrationService(t)
	ctx := context.Background()
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := &registrationWorker{cancel: cancel, done: make(chan struct{}), cleanupErr: errors.New("fixture failed drain")}
	close(worker.done)
	m.registration.workers["fixture"] = worker
	if err := m.ShutdownRegistrations(ctx); !errors.Is(err, worker.cleanupErr) {
		t.Fatalf("failed drain=%v", err)
	}
	if m.RegistrationContractAvailable() {
		t.Fatal("failed drain advertised capability")
	}
	// The synthetic worker has no process; remove it after asserting its retained failure.
	delete(m.registration.workers, "fixture")
	if len(commands()) != 0 {
		t.Fatal("failed-drain fixture unexpectedly launched a child")
	}
}

func TestTransportOwnedStatefulSessionSurvivesRequestAndRetires(t *testing.T) {
	root, commands := transportOwnedRegistrationHarness(t)
	m, _ := registrationService(t)
	s := transportOwnedRegistrationFixture(t, root, "owned-stateful", nil)
	s.SessionMode = types.SessionModeStateful
	input := registrationInputFor(t, s)
	if err := m.RegisterManagedMcpServer(context.Background(), input, s); err != nil {
		t.Fatal(err)
	}
	assertOwnedTransportWait(t, commands())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := m.sessionManager.GetOrCreateSession(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()
	if _, err := c.ListTools(callCtx, protocol.ListToolsRequest{}); err != nil {
		t.Fatalf("request cancellation terminated the long-lived session: %v", err)
	}
	if len(commands()) != 2 {
		t.Fatalf("expected discovery and stateful commands, got %d", len(commands()))
	}
	if err := m.DeregisterMcpServerContext(context.Background(), s.Name); err != nil {
		t.Fatal(err)
	}
	// Deregistration synchronizes the transport-owned close before reading ProcessState.
	assertOwnedTransportWait(t, commands())
	if m.sessionManager.HasSession(s.Name) {
		t.Fatal("terminal retirement retained the session")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("repeated close: %v", err)
	}
	if outcome, err := m.ResolveRegistration(context.Background(), s.Name, input); err != nil || outcome != "absent" {
		t.Fatalf("terminal receipt before shutdown: %q %v", outcome, err)
	}
	assertOwnedTransportWait(t, commands())
}

func TestTransportOwnedLongLivedInitializeCancellationReaps(t *testing.T) {
	root, commands := transportOwnedRegistrationHarness(t)
	s := transportOwnedRegistrationFixture(t, root, "owned-long-lived-init", map[string]string{"MCPJUNGLE_OWNED_NO_INIT": "1"})
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if c, err := runStdioServer(ctx, s, 2); err == nil || c != nil {
		t.Fatal("canceled initialization acknowledged a live session")
	} else {
		var cleanup *ConnectionCleanupError
		if errors.As(err, &cleanup) {
			t.Fatalf("transport Wait is wait_unknown: %v", err)
		}
	}
	assertOwnedTransportWait(t, commands())
}

func TestTransportOwnedFailedSpawnDoesNotClaimWait(t *testing.T) {
	root, commands := transportOwnedRegistrationHarness(t)
	s, err := model.NewStdioServer("owned-missing", "", filepath.Join(root, "missing"), nil, nil, types.SessionModeStateful)
	if err != nil {
		t.Fatal(err)
	}
	if c, err := runStdioServer(context.Background(), s, 2); err == nil || c != nil {
		t.Fatal("failed spawn acknowledged a live session")
	}
	if len(commands()) != 1 || commands()[0].Process != nil || commands()[0].ProcessState != nil {
		t.Fatal("failed spawn claimed a started or waited process")
	}
}

func TestTransportOwnedActiveCallCancellationPreservesSessionUntilClose(t *testing.T) {
	root, commands := transportOwnedRegistrationHarness(t)
	s := transportOwnedRegistrationFixture(t, root, "owned-call", map[string]string{"MCPJUNGLE_OWNED_BLOCK_CALL": "1"})
	c, err := runStdioServer(context.Background(), s, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	request := protocol.CallToolRequest{}
	request.Params.Name = "echo"
	if _, err = c.CallTool(ctx, request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("active call cancellation=%v", err)
	}
	if _, err = os.Stat(filepath.Join(root, "calling")); err != nil {
		t.Fatal("cancellation did not reach the actual backend call")
	}
	fresh, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if _, err = c.ListTools(fresh, protocol.ListToolsRequest{}); err != nil {
		t.Fatalf("canceled call terminated long-lived backend: %v", err)
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	assertOwnedTransportWait(t, commands())
}
