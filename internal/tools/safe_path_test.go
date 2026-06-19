package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSafePath_BlocksTraversal 验证 safePath 拒绝路径遍历攻击。
func TestSafePath_BlocksTraversal(t *testing.T) {
	workDir := "/Users/zsa/Desktop/harness/harness9"
	cases := []string{
		"../../etc/passwd",
		"../escaped.txt",
		"src/../../escaped.txt",
	}
	for _, p := range cases {
		_, err := safePath(workDir, p)
		if err == nil {
			t.Errorf("safePath(%q, %q) should have returned error", workDir, p)
		}
	}
}

// TestSafePath_AllowsInsideWorkDir 验证 safePath 接受合法路径。
func TestSafePath_AllowsInsideWorkDir(t *testing.T) {
	workDir := "/project"
	cases := map[string]string{
		"src/main.go": "/project/src/main.go",
		"./README.md": "/project/README.md",
		"a/b/c.txt":   "/project/a/b/c.txt",
		"":            "/project",
	}
	for in, want := range cases {
		got, err := safePath(workDir, in)
		if err != nil {
			t.Errorf("safePath(%q, %q) unexpected error: %v", workDir, in, err)
			continue
		}
		if got != want {
			t.Errorf("safePath(%q, %q) = %q, want %q", workDir, in, got, want)
		}
	}
}

// TestSafePath_AbsolutePathInsideWorkDir 验证：当输入已是 workDir 下的绝对路径时，
// safePath 直接返回该绝对路径，而不会与 workDir 拼接成翻倍路径（回归测试）。
// 这是 SWE-bench 轨迹中 10/12 实例命中的 read_file/edit_file "no such file" 根因。
func TestSafePath_AbsolutePathInsideWorkDir(t *testing.T) {
	workDir := "/var/folders/rj/swebench-abc123"
	cases := map[string]string{
		// 绝对路径恰好在 workDir 下 → 原样返回，绝不翻倍
		"/var/folders/rj/swebench-abc123/src/flask/cli.py": "/var/folders/rj/swebench-abc123/src/flask/cli.py",
		"/var/folders/rj/swebench-abc123":                  "/var/folders/rj/swebench-abc123",
		"/var/folders/rj/swebench-abc123/a/b/../c.py":      "/var/folders/rj/swebench-abc123/a/c.py",
		// 相对路径仍正常拼接
		"src/flask/cli.py": "/var/folders/rj/swebench-abc123/src/flask/cli.py",
	}
	for in, want := range cases {
		got, err := safePath(workDir, in)
		if err != nil {
			t.Errorf("safePath(%q, %q) unexpected error: %v", workDir, in, err)
			continue
		}
		if got != want {
			t.Errorf("safePath(%q, %q) = %q, want %q (path-doubling regression?)", workDir, in, got, want)
		}
	}
}

// TestSafePath_AbsolutePathOutsideWorkDir 验证绝对路径逃逸 workDir 时仍被拒绝。
func TestSafePath_AbsolutePathOutsideWorkDir(t *testing.T) {
	workDir := "/var/folders/rj/swebench-abc123"
	cases := []string{
		"/etc/passwd",
		"/var/folders/rj/swebench-abc123-evil/x.py", // 前缀相近但非子目录
		"/var/folders/rj/other/y.py",
	}
	for _, p := range cases {
		if _, err := safePath(workDir, p); err == nil {
			t.Errorf("safePath(%q, %q) should reject out-of-workdir absolute path", workDir, p)
		}
	}
}

func TestSafePath_SensitivePathBlocked(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}
	cases := []struct {
		inputPath string
	}{
		{filepath.Join(home, ".ssh", "id_rsa")},
		{filepath.Join(home, ".aws", "credentials")},
		{filepath.Join(home, ".kube", "config")},
		{filepath.Join(home, ".gnupg", "secring.gpg")},
		{filepath.Join(home, ".netrc")},
	}
	for _, tc := range cases {
		_, err := safePath("/tmp", tc.inputPath)
		if err == nil {
			t.Errorf("safePath should reject sensitive path %s", tc.inputPath)
		}
	}
}

// TestWriteFileTool_RejectsPathTraversal 验证 WriteFileTool 拒绝逃逸工作区的写入请求。
func TestWriteFileTool_RejectsPathTraversal(t *testing.T) {
	tmp := t.TempDir()
	tool := NewWriteFileTool(tmp)

	args := json.RawMessage(`{"path":"../escaped.txt","content":"pwned"}`)
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for traversal path, got nil")
	}
	if !strings.Contains(err.Error(), "超出工作区范围") {
		t.Fatalf("expected sandbox error, got: %v", err)
	}
}
