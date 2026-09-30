package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	protocol "github.com/mark3labs/mcp-go/mcp"
)

type registrationInventoryReply struct {
	result string
	code   int
	text   string
	block  bool
}

type registrationInventoryTransport struct {
	registrationFixtureTransport
	replies map[string]registrationInventoryReply
	methods []string
}

func (f *registrationInventoryTransport) SendRequest(ctx context.Context, req transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
	if req.Method == "initialize" {
		return f.registrationFixtureTransport.SendRequest(ctx, req)
	}
	f.methods = append(f.methods, req.Method)
	if req.Method == "tools/list" {
		return &transport.JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"tools":[]}`)}, nil
	}
	reply, exists := f.replies[req.Method]
	if !exists {
		return nil, errors.New("unexpected fixture method " + req.Method)
	}
	if reply.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	response := &transport.JSONRPCResponse{JSONRPC: "2.0", ID: req.ID}
	if reply.code != 0 {
		response.Error = &protocol.JSONRPCErrorDetails{Code: reply.code, Message: reply.text}
	} else {
		response.Result = json.RawMessage(reply.result)
	}
	return response, nil
}

func inventoryClient(t *testing.T, capabilities string, replies map[string]registrationInventoryReply) (*client.Client, *registrationInventoryTransport) {
	t.Helper()
	fixture := &registrationInventoryTransport{registrationFixtureTransport: registrationFixtureTransport{capability: capabilities}, replies: replies}
	c := client.NewClient(fixture)
	request := protocol.InitializeRequest{}
	request.Params.ProtocolVersion = protocol.LATEST_PROTOCOL_VERSION
	if _, err := c.Initialize(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, fixture
}

func TestManagedInactiveResourcePlaceholderMatchesShadcn(t *testing.T) {
	c, fixture := inventoryClient(t, `,"resources":{},"logging":{}`, map[string]registrationInventoryReply{
		"resources/list":           {code: protocol.METHOD_NOT_FOUND, text: "Method not found"},
		"resources/templates/list": {code: protocol.METHOD_NOT_FOUND, text: "Method not found"},
	})
	if _, err := discoverManagedTools(context.Background(), c); err != nil {
		t.Fatalf("inactive resources placeholder rejected: %v", err)
	}
	if !reflect.DeepEqual(fixture.methods, []string{"resources/list", "resources/templates/list", "tools/list"}) {
		t.Fatalf("both inventories must be proved inactive before tools: %v", fixture.methods)
	}
}

func TestManagedInactiveInventoryCompatibility(t *testing.T) {
	empty := map[string]registrationInventoryReply{
		"resources/list":           {result: `{"resources":[]}`},
		"resources/templates/list": {result: `{"resourceTemplates":[]}`},
		"prompts/list":             {result: `{"prompts":[]}`},
	}
	cases := []struct {
		name, capabilities, method string
		reply                      registrationInventoryReply
		valid                      bool
	}{
		{name: "empty-both-inventories", capabilities: `,"resources":{}`, valid: true},
		{name: "empty-prompts", capabilities: `,"prompts":{}`, valid: true},
		{name: "empty-all-with-tools-changes", capabilities: `,"resources":{},"prompts":{}`, valid: true},
		{name: "mixed-unimplemented-and-empty", capabilities: `,"resources":{}`, method: "resources/list", reply: registrationInventoryReply{code: protocol.METHOD_NOT_FOUND}, valid: true},
		{name: "unimplemented-prompts", capabilities: `,"prompts":{}`, method: "prompts/list", reply: registrationInventoryReply{code: protocol.METHOD_NOT_FOUND}, valid: true},
		{name: "valid-empty-cursor-and-meta", capabilities: `,"resources":{}`, method: "resources/list", reply: registrationInventoryReply{result: `{"resources":[],"nextCursor":"","_meta":{"public":true}}`}, valid: true},
		{name: "nonempty-resource", capabilities: `,"resources":{}`, method: "resources/list", reply: registrationInventoryReply{result: `{"resources":[{"uri":"fixture://active","name":"active"}]}`}},
		{name: "nonempty-template-after-empty-resources", capabilities: `,"resources":{}`, method: "resources/templates/list", reply: registrationInventoryReply{result: `{"resourceTemplates":[{"uriTemplate":"fixture://{id}","name":"active"}]}`}},
		{name: "nonempty-prompt", capabilities: `,"prompts":{}`, method: "prompts/list", reply: registrationInventoryReply{result: `{"prompts":[{"name":"active"}]}`}},
		{name: "resource-subscribe", capabilities: `,"resources":{"subscribe":true}`},
		{name: "resource-list-changed", capabilities: `,"resources":{"listChanged":true}`},
		{name: "prompt-list-changed", capabilities: `,"prompts":{"listChanged":true}`},
		{name: "missing-inventory", capabilities: `,"resources":{}`, method: "resources/list", reply: registrationInventoryReply{result: `{}`}},
		{name: "null-result", capabilities: `,"resources":{}`, method: "resources/list", reply: registrationInventoryReply{result: `null`}},
		{name: "null-inventory", capabilities: `,"resources":{}`, method: "resources/list", reply: registrationInventoryReply{result: `{"resources":null}`}},
		{name: "object-inventory", capabilities: `,"resources":{}`, method: "resources/list", reply: registrationInventoryReply{result: `{"resources":{}}`}},
		{name: "nonempty-cursor", capabilities: `,"resources":{}`, method: "resources/list", reply: registrationInventoryReply{result: `{"resources":[],"nextCursor":"later"}`}},
		{name: "null-cursor", capabilities: `,"resources":{}`, method: "resources/list", reply: registrationInventoryReply{result: `{"resources":[],"nextCursor":null}`}},
		{name: "wrong-cursor-type", capabilities: `,"resources":{}`, method: "resources/list", reply: registrationInventoryReply{result: `{"resources":[],"nextCursor":7}`}},
		{name: "malformed-json", capabilities: `,"resources":{}`, method: "resources/list", reply: registrationInventoryReply{result: `{"resources":[]`}},
		{name: "duplicate-inventory", capabilities: `,"resources":{}`, method: "resources/list", reply: registrationInventoryReply{result: `{"resources":[],"resources":[]}`}},
		{name: "unexpected-shape", capabilities: `,"resources":{}`, method: "resources/list", reply: registrationInventoryReply{result: `{"resources":[],"other":[]}`}},
		{name: "null-meta", capabilities: `,"resources":{}`, method: "resources/list", reply: registrationInventoryReply{result: `{"resources":[],"_meta":null}`}},
		{name: "other-structured-error", capabilities: `,"resources":{}`, method: "resources/list", reply: registrationInventoryReply{code: protocol.INTERNAL_ERROR, text: "Method not found"}},
		{name: "text-only-method-not-found", capabilities: `,"resources":{}`, method: "resources/list", reply: registrationInventoryReply{result: `"Method not found"`}},
		{name: "timeout", capabilities: `,"resources":{}`, method: "resources/templates/list", reply: registrationInventoryReply{block: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			replies := make(map[string]registrationInventoryReply)
			for method, reply := range empty {
				replies[method] = reply
			}
			if tc.method != "" {
				replies[tc.method] = tc.reply
			}
			c, fixture := inventoryClient(t, tc.capabilities, replies)
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			_, err := discoverManagedTools(ctx, c)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v methods=%v", tc.valid, err, fixture.methods)
			}
			if !tc.valid {
				for _, method := range fixture.methods {
					if method == "tools/list" {
						t.Fatal("tools discovered before non-tool inactivity was established")
					}
				}
			}
		})
	}
}
