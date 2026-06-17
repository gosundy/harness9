package tools

import (
	"context"
	"encoding/json"
	"testing"
)

func TestMCPToolAdapter_Name(t *testing.T) {
	tests := []struct {
		serverName string
		toolName   string
		wantName   string
	}{
		{"context7", "resolve-library-id", "mcp__context7__resolve_library_id"},
		{"my-server", "my_tool", "mcp__my_server__my_tool"},
		{"server", "tool", "mcp__server__tool"},
	}
	for _, tc := range tests {
		adapter := NewMCPToolAdapter(tc.serverName, tc.toolName, "desc", nil, nil)
		if got := adapter.Name(); got != tc.wantName {
			t.Errorf("Name() = %q, want %q", got, tc.wantName)
		}
	}
}

func TestMCPToolAdapter_Definition(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`)
	adapter := NewMCPToolAdapter("srv", "tool", "my desc", schema, nil)
	def := adapter.Definition()
	if def.Name != "mcp__srv__tool" {
		t.Errorf("Definition().Name = %q", def.Name)
	}
	if def.Description == "" {
		t.Error("Definition().Description is empty")
	}
	if def.InputSchema == nil {
		t.Error("Definition().InputSchema is nil")
	}
}

func TestMCPToolAdapter_Execute(t *testing.T) {
	called := false
	var gotArgs json.RawMessage
	caller := MCPCallerFn(func(ctx context.Context, args json.RawMessage) (string, error) {
		called = true
		gotArgs = args
		return "result", nil
	})
	adapter := NewMCPToolAdapter("srv", "tool", "desc", nil, caller)
	args := json.RawMessage(`{"key":"value"}`)
	out, err := adapter.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if out != "result" {
		t.Errorf("Execute() = %q, want %q", out, "result")
	}
	if !called {
		t.Error("caller was not called")
	}
	if string(gotArgs) != string(args) {
		t.Errorf("caller got args %q, want %q", gotArgs, args)
	}
}

func TestSanitizeMCPName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"context7", "context7"},
		{"my-server", "my_server"},
		{"tool.name", "tool_name"},
		{"a b c", "a_b_c"},
	}
	for _, tc := range tests {
		if got := SanitizeMCPName(tc.in); got != tc.want {
			t.Errorf("SanitizeMCPName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
