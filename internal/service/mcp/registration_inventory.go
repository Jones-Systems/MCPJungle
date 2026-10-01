package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	protocol "github.com/mark3labs/mcp-go/mcp"
	"github.com/mcpjungle/mcpjungle/pkg/apierrors"
)

// requireInactiveInventory proves a declared placeholder has no inventory or continuation to publish.
func requireInactiveInventory(ctx context.Context, c *client.Client, method, inventory string) error {
	requestID := "registration-inventory-" + method
	response, err := c.GetTransport().SendRequest(ctx, transport.JSONRPCRequest{JSONRPC: "2.0", ID: protocol.NewRequestId(requestID), Method: method, Params: map[string]any{}})
	if err != nil {
		return fmt.Errorf("%s inactivity check: %w", method, err)
	}
	invalid := func(reason string) error {
		return fmt.Errorf("%s is not an inactive inventory: %s: %w", method, reason, apierrors.ErrInvalidInput)
	}
	if response == nil {
		return invalid("missing response")
	}
	id, validID := response.ID.Value().(string)
	if response.JSONRPC != "2.0" || !validID || id != requestID {
		return invalid("invalid response envelope")
	}
	if response.Error != nil {
		if response.Error.Code == protocol.METHOD_NOT_FOUND && len(response.Result) == 0 {
			return nil
		}
		return invalid(fmt.Sprintf("error code %d", response.Error.Code))
	}
	decoder := json.NewDecoder(bytes.NewReader(response.Result))
	decoder.UseNumber()
	value, err := readUniqueJSON(decoder)
	if err != nil {
		return invalid("malformed result")
	}
	if _, err = decoder.Token(); err != io.EOF {
		return invalid("trailing result")
	}
	object, ok := value.(map[string]any)
	if !ok {
		return invalid("result must be an object")
	}
	entries, ok := object[inventory].([]any)
	if !ok || len(entries) != 0 {
		return invalid("inventory must be an empty array")
	}
	for key, value := range object {
		switch key {
		case inventory:
		case "nextCursor":
			cursor, ok := value.(string)
			if !ok || cursor != "" {
				return invalid("continuation is not empty")
			}
		case "_meta":
			if _, ok := value.(map[string]any); !ok {
				return invalid("metadata must be an object")
			}
		default:
			return invalid("unexpected result field")
		}
	}
	return ctx.Err()
}
