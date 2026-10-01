package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mcpjungle/mcpjungle/internal/model"
	"github.com/mcpjungle/mcpjungle/pkg/types"
	"golang.org/x/sys/unix"
)

type inventoryProcessReply struct {
	Result any  `json:"result"`
	Code   int  `json:"code"`
	Block  bool `json:"block"`
}

type inventoryProcessPlan struct {
	Capabilities map[string]any                   `json:"capabilities"`
	Replies      map[string]inventoryProcessReply `json:"replies"`
}

func TestRegistrationInventoryStdioFixture(t *testing.T) {
	if os.Getenv("MCPJUNGLE_INVENTORY_FIXTURE") != "1" {
		return
	}
	root := os.Getenv("MCPJUNGLE_INVENTORY_ROOT")
	data, err := os.ReadFile(filepath.Join(root, "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	var plan inventoryProcessPlan
	if err = json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "pid"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "generation"), []byte(fixtureGeneration(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil || len(req.ID) == 0 {
			continue
		}
		if req.Method == "tools/list" {
			_ = os.WriteFile(filepath.Join(root, "tools-listed"), []byte("1"), 0600)
		}
		response := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		switch req.Method {
		case "initialize":
			response["result"] = map[string]any{"protocolVersion": "2025-03-26", "serverInfo": map[string]string{"name": "inventory-fixture", "version": "1"}, "capabilities": plan.Capabilities}
		case "tools/list":
			response["result"] = map[string]any{"tools": []any{map[string]any{"name": "echo", "inputSchema": map[string]any{"type": "object"}}}}
		default:
			reply := plan.Replies[req.Method]
			if reply.Block {
				for {
					time.Sleep(10 * time.Millisecond)
				}
			}
			if reply.Code != 0 {
				response["error"] = map[string]any{"code": reply.Code, "message": "Method not found"}
			} else {
				response["result"] = reply.Result
			}
		}
		data, _ := json.Marshal(response)
		fmt.Println(string(data))
	}
	os.Exit(0)
}

func inventoryProcessFixture(t *testing.T, name string, plan inventoryProcessPlan) (*model.McpServer, string) {
	t.Helper()
	root := t.TempDir()
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "plan.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s, err := model.NewStdioServer(name, "", executable, []string{"-test.run=^TestRegistrationInventoryStdioFixture$"}, map[string]string{"MCPJUNGLE_INVENTORY_FIXTURE": "1", "MCPJUNGLE_INVENTORY_ROOT": root}, types.SessionModeStateless)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		data, err := os.ReadFile(filepath.Join(root, "pid"))
		if err != nil {
			return
		}
		pid, _ := strconv.Atoi(string(data))
		generation, err := os.ReadFile(filepath.Join(root, "generation"))
		if err != nil || len(generation) == 0 || fixtureGeneration(pid) != string(generation) {
			return
		}
		fd, err := unix.PidfdOpen(pid, 0)
		if err == nil {
			if fixtureGeneration(pid) == string(generation) {
				_ = unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0)
			}
			_ = unix.Close(fd)
		}
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) && fixtureGeneration(pid) == string(generation) {
			time.Sleep(5 * time.Millisecond)
		}
		if fixtureGeneration(pid) == string(generation) {
			t.Error("owned inventory fixture generation remains after cleanup")
		}
	})
	return s, root
}

func TestManagedInactiveInventoryRegistrationDrainsProcessAndNeverPublishesUnsupportedCatalog(t *testing.T) {
	for _, scenario := range []string{"shadcn", "empty-with-tool-changes", "resource-subscribe", "prompt-list-changed", "nonempty-template", "wrong-error", "null-inventory", "cursor", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			m, db := registrationService(t)
			plan := inventoryProcessPlan{Capabilities: map[string]any{"tools": map[string]any{}, "resources": map[string]any{}}, Replies: map[string]inventoryProcessReply{
				"resources/list":           {Result: map[string]any{"resources": []any{}}},
				"resources/templates/list": {Result: map[string]any{"resourceTemplates": []any{}}},
				"prompts/list":             {Result: map[string]any{"prompts": []any{}}},
			}}
			valid := scenario == "shadcn" || scenario == "empty-with-tool-changes"
			switch scenario {
			case "shadcn":
				plan.Capabilities["logging"] = map[string]any{}
				plan.Replies["resources/list"] = inventoryProcessReply{Code: -32601}
				plan.Replies["resources/templates/list"] = inventoryProcessReply{Code: -32601}
			case "empty-with-tool-changes":
				plan.Capabilities["tools"] = map[string]any{"listChanged": true}
				plan.Capabilities["prompts"] = map[string]any{}
			case "resource-subscribe":
				plan.Capabilities["resources"] = map[string]any{"subscribe": true}
			case "prompt-list-changed":
				plan.Capabilities["prompts"] = map[string]any{"listChanged": true}
			case "nonempty-template":
				plan.Replies["resources/templates/list"] = inventoryProcessReply{Result: map[string]any{"resourceTemplates": []any{map[string]any{"uriTemplate": "fixture://{id}", "name": "active"}}}}
			case "wrong-error":
				plan.Replies["resources/templates/list"] = inventoryProcessReply{Code: -32603}
			case "null-inventory":
				plan.Replies["resources/list"] = inventoryProcessReply{Result: map[string]any{"resources": nil}}
			case "cursor":
				plan.Replies["resources/templates/list"] = inventoryProcessReply{Result: map[string]any{"resourceTemplates": []any{}, "nextCursor": "later"}}
			case "timeout":
				plan.Replies["resources/templates/list"] = inventoryProcessReply{Block: true}
			}
			s, root := inventoryProcessFixture(t, "inventory-"+scenario, plan)
			ctx := context.Background()
			cancel := func() {}
			if scenario == "timeout" {
				ctx, cancel = context.WithTimeout(ctx, 300*time.Millisecond)
			}
			defer cancel()
			err := m.RegisterManagedMcpServer(ctx, registrationInputFor(t, s), s)
			if (err == nil) != valid {
				t.Fatalf("valid=%v err=%v", valid, err)
			}
			for _, table := range []string{"prompts", "resources"} {
				var count int64
				if err := db.Table(table).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("unsupported %s catalog published: count=%d err=%v", table, count, err)
				}
			}
			if !valid {
				var count int64
				if err := db.Model(&model.McpServer{}).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("failed inventory registration committed server: count=%d err=%v", count, err)
				}
				if tools, err := m.ListTools(); err != nil || len(tools) != 0 || m.mcpProxyServer.GetTool(s.Name+"__echo") != nil {
					t.Fatal("failed inventory registration published tools")
				}
				if _, err := os.Stat(filepath.Join(root, "tools-listed")); !os.IsNotExist(err) {
					t.Fatal("tools discovered before inactive inventories were established")
				}
			}
			data, err := os.ReadFile(filepath.Join(root, "pid"))
			if err != nil {
				t.Fatal(err)
			}
			pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
			generation, err := os.ReadFile(filepath.Join(root, "generation"))
			if err != nil || len(generation) == 0 || fixtureGeneration(pid) == string(generation) {
				t.Fatal("managed registration returned with its discovery child still present")
			}
		})
	}
}
