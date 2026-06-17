package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/harness9/internal/schema"
)

// MCPCallerFn is the function signature for MCP tool calls.
type MCPCallerFn func(ctx context.Context, args json.RawMessage) (string, error)

// MCPToolAdapter wraps an MCP tool as a BaseTool.
type MCPToolAdapter struct {
	adapterName string
	def         schema.ToolDefinition
	caller      MCPCallerFn
}

// NewMCPToolAdapter creates a new MCPToolAdapter.
func NewMCPToolAdapter(serverName, toolName, description string, inputSchema json.RawMessage, caller MCPCallerFn) *MCPToolAdapter {
	name := "mcp__" + sanitizeMCPName(serverName) + "__" + sanitizeMCPName(toolName)
	return &MCPToolAdapter{
		adapterName: name,
		def: schema.ToolDefinition{
			Name:        name,
			Description: fmt.Sprintf("[MCP:%s] %s", serverName, description),
			InputSchema: parseInputSchema(inputSchema),
		},
		caller: caller,
	}
}

// Name returns the tool's namespaced identifier.
func (a *MCPToolAdapter) Name() string { return a.adapterName }

// Definition returns the tool's schema definition.
func (a *MCPToolAdapter) Definition() schema.ToolDefinition { return a.def }

// Execute forwards the call to the MCP server via the caller function.
func (a *MCPToolAdapter) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return a.caller(ctx, args)
}

// sanitizeMCPName replaces non-alphanumeric/underscore characters with underscores.
func sanitizeMCPName(name string) string {
	var sb strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	return sb.String()
}

// parseInputSchema deserializes a raw JSON schema into a map for use in ToolDefinition.
func parseInputSchema(raw json.RawMessage) any {
	if len(raw) == 0 {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return s
}
