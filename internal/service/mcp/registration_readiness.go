package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	protocol "github.com/mark3labs/mcp-go/mcp"
	"github.com/mcpjungle/mcpjungle/internal/model"
	"github.com/mcpjungle/mcpjungle/pkg/apierrors"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func managedToolModel(tool protocol.Tool) (model.Tool, error) {
	if tool.Name == "" {
		return model.Tool{}, fmt.Errorf("empty tool name")
	}
	schema := tool.RawInputSchema
	if schema == nil {
		var err error
		schema, err = json.Marshal(tool.InputSchema)
		if err != nil {
			return model.Tool{}, err
		}
	}
	var decoded protocol.ToolInputSchema
	if err := json.Unmarshal(schema, &decoded); err != nil {
		return model.Tool{}, err
	}
	annotations, err := json.Marshal(tool.Annotations)
	if err != nil {
		return model.Tool{}, err
	}
	definition, err := json.Marshal(tool)
	if err != nil {
		return model.Tool{}, err
	}
	return model.Tool{Name: tool.Name, Description: tool.Description, InputSchema: datatypes.JSON(schema), Annotations: annotations, Definition: definition, Enabled: true}, nil
}

func (m *MCPService) visibleToolsQuery(db *gorm.DB) *gorm.DB {
	if m.registration != nil && m.registration.storage {
		return db.Where("server_id NOT IN (?)", m.db.Model(&model.RegistrationLifecycle{}).Select("server_id").Where("state <> ? OR fenced = ?", "ready", true))
	}
	return db
}
func (m *MCPService) registrationReady(ctx context.Context, name string) error {
	if m.registration == nil || !m.registration.storage {
		return nil
	}
	row, err := m.lifecycle(ctx, name)
	if err != nil {
		return err
	}
	if row != nil && (row.Fenced || row.State != "ready") {
		return fmt.Errorf("registration is not ready: %w", apierrors.ErrNotFound)
	}
	return nil
}
func (m *MCPService) registrationToolFilter(ctx context.Context, tools []protocol.Tool) []protocol.Tool {
	visible := make([]protocol.Tool, 0, len(tools))
	allowed := map[string]bool{}
	for _, tool := range tools {
		name, _, ok := splitServerToolName(tool.Name)
		if !ok {
			continue
		}
		ready, found := allowed[name]
		if !found {
			ready = m.registrationReady(ctx, name) == nil
			allowed[name] = ready
		}
		if ready {
			visible = append(visible, tool)
		}
	}
	return visible
}

func decodeManagedToolDefinition(data []byte) (protocol.Tool, error) {
	var tool protocol.Tool
	if err := json.Unmarshal(data, &tool); err != nil {
		return protocol.Tool{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return protocol.Tool{}, err
	}
	schema, ok := fields["inputSchema"]
	if !ok {
		return protocol.Tool{}, fmt.Errorf("tool input schema missing")
	}
	var object map[string]any
	if err := json.Unmarshal(schema, &object); err != nil || object == nil {
		return protocol.Tool{}, fmt.Errorf("tool input schema must be an object")
	}
	tool.InputSchema = protocol.ToolInputSchema{}
	tool.RawInputSchema = schema
	if schema, ok = fields["outputSchema"]; ok {
		if err := json.Unmarshal(schema, &object); err != nil || object == nil {
			return protocol.Tool{}, fmt.Errorf("tool output schema must be an object")
		}
		tool.OutputSchema = protocol.ToolOutputSchema{}
		tool.RawOutputSchema = schema
	}
	return tool, nil
}
