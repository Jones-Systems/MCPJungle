//go:build qualification

package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/mcpjungle/mcpjungle/internal/service/mcp"
)

func TestRegistrationOwnedAdapterEOFQualification(t *testing.T) {
	adapter := os.Getenv("MCPJUNGLE_QUALIFICATION_ADAPTER")
	python := os.Getenv("MCPJUNGLE_QUALIFICATION_PYTHON")
	receipt := os.Getenv("MCPJUNGLE_QUALIFICATION_RECEIPT")
	if adapter == "" || python == "" || receipt == "" {
		t.Skip("explicit adapter, Python and durable receipt paths required")
	}
	for _, path := range []string{adapter, python, receipt} {
		if !filepath.IsAbs(path) {
			t.Fatal("qualification input paths must be absolute")
		}
	}
	harness, err := filepath.Abs("../../qualification/eof_harness.py")
	if err != nil {
		t.Fatal(err)
	}
	scratch, err := os.MkdirTemp("", "mcpjungle-api-owned-")
	if err != nil {
		t.Fatal(err)
	}
	qualified := false
	t.Cleanup(func() {
		if qualified {
			if err := os.RemoveAll(scratch); err != nil {
				t.Errorf("owned qualification scratch cleanup: %v", err)
			}
		}
	})
	for _, name := range []string{"home", "tmp"} {
		if err = os.Mkdir(filepath.Join(scratch, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", filepath.Join(scratch, "home"))
	t.Setenv("TMPDIR", filepath.Join(scratch, "tmp"))
	t.Chdir(scratch)
	commands, restore := mcp.ObserveRegistrationCommandsForQualification()
	t.Cleanup(restore)
	router := registrationAPIFixture(t, true)
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-B", harness, "--gateway-url", gateway.URL,
		"--adapter-script", adapter, "--python", python, "--receipt", receipt, "--scratch-parent", scratch)
	cmd.Env = []string{"HOME=" + scratch, "TMPDIR=" + scratch, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "PYTHONDONTWRITEBYTECODE=1"}
	cmd.Dir = scratch
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 10 * time.Second
	output, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("EOF harness failed (%v); receipt=%s scratch=%s output=%s", runErr, receipt, scratch, output)
	}
	data, err := os.ReadFile(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err = json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result["passed"] != true || result["terminal_before_gateway_shutdown"] != true || result["backend_called_before_eof"] != true {
		t.Fatalf("terminal cleanup unproved; gateway Wait remains wait_unknown: %s", data)
	}
	// The terminal HTTP response follows transport close and worker completion.
	// Reading ProcessState is safe only after that synchronization, never by polling.
	observed := commands()
	if len(observed) < 2 {
		t.Fatal("actual discovery and stateful backend creation not observed")
	}
	waits := make([]map[string]any, 0, len(observed))
	for _, command := range observed {
		if command.Process == nil || command.ProcessState == nil {
			t.Fatal("gateway transport Wait is wait_unknown")
		}
		status, ok := command.ProcessState.Sys().(syscall.WaitStatus)
		if !ok || (!status.Exited() && !status.Signaled()) {
			t.Fatal("gateway transport Wait lacks terminal status")
		}
		waits = append(waits, map[string]any{"pid": command.Process.Pid, "exit_code": command.ProcessState.ExitCode(), "exited": status.Exited(), "signaled": status.Signaled()})
	}
	result["gateway_wait"] = "transport_owned_wait_observed_after_terminal_api_response"
	result["gateway_waits"] = waits
	result["harness_wait"] = cmd.ProcessState.ExitCode()
	result["gateway_shutdown_started_at_wait_observation"] = false
	data, err = json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(receipt, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	qualified = true
	t.Logf("actual idle EOF: %d gateway transport Wait results; frontend and cleanup worker Wait; terminal receipt before shutdown: %s", len(waits), receipt)
}
