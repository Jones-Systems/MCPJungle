package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mark3labs/mcp-go/server"
	"github.com/mcpjungle/mcpjungle/internal/migrations"
	"github.com/mcpjungle/mcpjungle/internal/model"
	"github.com/mcpjungle/mcpjungle/internal/service/config"
	"github.com/mcpjungle/mcpjungle/internal/service/mcp"
	"github.com/mcpjungle/mcpjungle/internal/telemetry"
	"gorm.io/gorm"
)

func registrationAPIFixture(t *testing.T, guard bool, seeds ...func(*gorm.DB)) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
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
	for _, seed := range seeds {
		seed(db)
	}
	proxy := server.NewMCPServer("fixture", "1", server.WithToolCapabilities(false))
	svc, err := mcp.NewMCPService(&mcp.ServiceConfig{DB: db, RegistrationSingleWriter: guard, McpServerInitReqTimeout: 2, McpProxyServer: proxy, SseMcpProxyServer: server.NewMCPServer("sse", "1"), Metrics: telemetry.NewNoopCustomMetrics(), SessionManager: mcp.NewSessionManager(&mcp.SessionManagerConfig{DB: db, IdleTimeoutSec: 0, InitReqTimeoutSec: 2})})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.ShutdownRegistrations(context.Background()); svc.Shutdown(); _ = sql.Close() })
	configuration := config.NewServerConfigService(db)
	if _, err := configuration.Init(model.ModeDev); err != nil {
		t.Fatal(err)
	}
	s := &Server{mcpService: svc, configService: configuration}
	router := gin.New()
	router.Any("/mcp", s.requireInitialized(), s.checkAuthForMcpProxyAccess(), gin.WrapH(server.NewStreamableHTTPServer(proxy)))
	router.GET("/api/v0/servers", s.listServersHandler())
	router.GET("/api/v0/prompts", s.listPromptsHandler())
	router.GET("/api/v0/resources", s.listResourcesHandler())
	router.GET("/api/v0/capabilities", s.registrationCapabilitiesHandler())
	router.GET("/api/v0/registration-status", s.registrationStatusHandler())
	router.POST("/api/v0/servers", s.registerServerHandler())
	router.POST("/api/v0/servers/:name/resolve", s.resolveRegistrationHandler())
	router.DELETE("/api/v0/servers/:name", s.deregisterServerHandler())
	return router
}
func registrationAPIRequest(router *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	return w
}
func TestRegistrationCapabilityRequiresWriterGuard(t *testing.T) {
	for _, guard := range []bool{false, true} {
		r := registrationAPIFixture(t, guard)
		w := registrationAPIRequest(r, http.MethodGet, "/api/v0/capabilities", "")
		var response map[string]int
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		want := 0
		if guard {
			want = 1
		}
		if response["registration_contract"] != want {
			t.Fatalf("capability without proven guard: %s", w.Body)
		}
	}
}
func TestRegistrationResolveRetiresAbsentNameBeforeAnyCreate(t *testing.T) {
	r := registrationAPIFixture(t, true)
	definition := `{"name":"absent","transport":"stdio","command":"/definitely/missing"}`
	for i := 0; i < 2; i++ {
		w := registrationAPIRequest(r, http.MethodPost, "/api/v0/servers/absent/resolve", `{"registration":`+definition+`}`)
		if w.Code != http.StatusOK {
			t.Fatalf("resolve code=%d body=%s", w.Code, w.Body)
		}
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body["terminal"] != true || body["outcome"] != "absent" {
			t.Fatal("invalid terminal receipt")
		}
	}
	w := registrationAPIRequest(r, http.MethodPost, "/api/v0/servers?registration_contract=1", definition)
	if w.Code != http.StatusConflict {
		t.Fatalf("retired create status=%d body=%s", w.Code, w.Body)
	}
	w = registrationAPIRequest(r, http.MethodDelete, "/api/v0/servers/absent", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("managed delete status=%d body=%s", w.Code, w.Body)
	}
	w = registrationAPIRequest(r, http.MethodPost, "/api/v0/servers?registration_contract=1", definition)
	if w.Code != http.StatusConflict {
		t.Fatal("delete removed tombstone")
	}
}
func TestRegistrationContractRejectsAmbiguityAndForce(t *testing.T) {
	r := registrationAPIFixture(t, true)
	for _, body := range []string{`{"name":"x","name":"x","transport":"stdio","command":"/bin/a"}`, `{"name":"x","transport":"stdio","command":"/bin/a","unknown":null}`, `{"name":"x","transport":"stdio","command":"relative"}`} {
		w := registrationAPIRequest(r, http.MethodPost, "/api/v0/servers?registration_contract=1", body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid status=%d", w.Code)
		}
	}
	w := registrationAPIRequest(r, http.MethodPost, "/api/v0/servers?registration_contract=1&force=true", `{"name":"x","transport":"stdio","command":"/bin/a"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatal("force accepted")
	}
}

// This child has a valid initialization response but an indefinitely blocked discovery request.
func TestRegistrationAPIStdioFixture(t *testing.T) {
	if os.Getenv("MCPJUNGLE_API_FIXTURE") != "1" {
		return
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
			_ = os.WriteFile(filepath.Join(os.Getenv("MCPJUNGLE_API_ROOT"), "listing"), []byte("1"), 0600)
			for {
				time.Sleep(time.Hour)
			}
		}
		result := map[string]any{"protocolVersion": "2025-03-26", "serverInfo": map[string]string{"name": "fixture", "version": "1"}, "capabilities": map[string]any{"tools": map[string]any{}}}
		response, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		fmt.Println(string(response))
	}
	os.Exit(0)
}

func TestRegistrationHTTPDisconnectDrainsDiscovery(t *testing.T) {
	router := registrationAPIFixture(t, true)
	httpServer := httptest.NewServer(router)
	defer httpServer.Close()
	root := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	definition := map[string]any{"name": "disconnect", "transport": "stdio", "command": executable, "args": []string{"-test.run=^TestRegistrationAPIStdioFixture$"}, "env": map[string]string{"MCPJUNGLE_API_FIXTURE": "1", "MCPJUNGLE_API_ROOT": root}}
	data, _ := json.Marshal(definition)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL+"/api/v0/servers?registration_contract=1", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	finished := make(chan error, 1)
	go func() {
		response, err := http.DefaultClient.Do(req)
		if response != nil {
			response.Body.Close()
		}
		finished <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	observed := false
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(root, "listing")); err == nil {
			observed = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !observed {
		t.Fatal("HTTP fixture did not reach discovery")
	}
	cancel()
	if err = <-finished; err == nil {
		t.Fatal("cancelled HTTP create returned success")
	}
	response := registrationAPIRequest(router, http.MethodPost, "/api/v0/servers/disconnect/resolve", `{"registration":`+string(data)+`}`)
	if response.Code != http.StatusOK {
		t.Fatalf("resolve after disconnect status=%d body=%s", response.Code, response.Body)
	}
	var receipt map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &receipt)
	if receipt["terminal"] != true || receipt["outcome"] != "absent" {
		t.Fatal("disconnect did not drain before terminal receipt")
	}
}

func TestRegistrationStatusReportsPendingWithoutChangingCapabilities(t *testing.T) {
	r := registrationAPIFixture(t, true, func(db *gorm.DB) {
		if err := db.Create(&model.RegistrationLifecycle{Name: "pending-old", Definition: "private-credential-and-args", State: "reserved", CleanupPending: true}).Error; err != nil {
			t.Fatal(err)
		}
	})
	w := registrationAPIRequest(r, http.MethodGet, "/api/v0/registration-status", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var result mcp.RegistrationStatus
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || !result.Available || !result.Complete || len(result.CleanupPendingNames) != 1 || result.CleanupPendingNames[0] != "pending-old" {
		t.Fatalf("invalid status: %s %v", w.Body, err)
	}
	if bytes.Contains(w.Body.Bytes(), []byte("private")) {
		t.Fatal("definition leaked")
	}
	w = registrationAPIRequest(r, http.MethodGet, "/api/v0/capabilities", "")
	var caps map[string]int
	if err := json.Unmarshal(w.Body.Bytes(), &caps); err != nil || len(caps) != 3 || caps["registration_contract"] != 1 {
		t.Fatalf("capability contract changed: %s %v", w.Body, err)
	}
}
