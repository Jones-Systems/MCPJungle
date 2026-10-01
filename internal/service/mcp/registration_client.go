package mcp

import (
	"context"
	"errors"
	"os/exec"
	"sync"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mcpjungle/mcpjungle/internal/model"
	"github.com/mcpjungle/mcpjungle/pkg/types"
)

type ConnectionCleanupError struct{ Err error }

func (e *ConnectionCleanupError) Error() string { return "MCP client cleanup failed: " + e.Err.Error() }
func (e *ConnectionCleanupError) Unwrap() error { return e.Err }

type registrationClientCloser struct {
	client *client.Client
	once   sync.Once
	err    error
}

func (c *registrationClientCloser) close() error {
	c.once.Do(func() {
		c.err = c.client.Close()
		// An ExitError comes from Wait after the process exited and was reaped.
		var exited *exec.ExitError
		if errors.As(c.err, &exited) {
			c.err = nil
		}
	})
	return c.err
}
func closeOnContext(ctx context.Context, c *client.Client) (*registrationClientCloser, func() bool) {
	closer := &registrationClientCloser{client: c}
	stop := context.AfterFunc(ctx, func() { _ = closer.close() })
	return closer, stop
}

func (m *MCPService) createRegistrationConnection(ctx context.Context, s *model.McpServer, storedAuth bool) (*client.Client, error) {
	if s.Transport == types.TransportStdio {
		return runRegistrationStdioServer(ctx, s, m.mcpServerInitReqTimeoutSec)
	}
	return createMcpServerConnectionWithDB(ctx, m.db, s, m.mcpServerInitReqTimeoutSec, storedAuth)
}
