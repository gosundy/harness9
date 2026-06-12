package tools

import (
	"strings"
	"testing"
)

func TestIsSafeURL(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		wantErr     bool
		errContains string
	}{
		// 合法公网地址（IP literal，无需 DNS）
		{"valid http IP", "http://8.8.8.8/", false, ""},
		{"valid https IP", "https://8.8.8.8/path", false, ""},
		// 非法 scheme
		{"ftp rejected", "ftp://example.com", true, "unsupported scheme"},
		{"file rejected", "file:///etc/passwd", true, "unsupported scheme"},
		// userinfo 拒绝
		{"userinfo rejected", "http://user:pass@8.8.8.8/", true, "userinfo"},
		// loopback
		{"loopback 127.0.0.1", "http://127.0.0.1/secret", true, ""},
		{"loopback 127.1.2.3", "http://127.1.2.3/", true, ""},
		// 链路本地 + 云 metadata
		{"link-local 169.254.1.1", "http://169.254.1.1/", true, ""},
		{"aws metadata", "http://169.254.169.254/latest/meta-data", true, ""},
		// RFC1918
		{"RFC1918 10.x", "http://10.0.0.1/", true, ""},
		{"RFC1918 172.16.x", "http://172.16.0.1/", true, ""},
		{"RFC1918 172.31.x", "http://172.31.255.254/", true, ""},
		{"RFC1918 192.168.x", "http://192.168.1.100/", true, ""},
		// CGNAT
		{"CGNAT 100.64.x", "http://100.64.0.1/", true, ""},
		// DNS fail-closed
		{"unresolvable domain", "http://does-not-exist.invalid/", true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := isSafeURL(tt.url)
			if tt.wantErr && err == nil {
				t.Errorf("expected error for %s, got nil", tt.url)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for %s: %v", tt.url, err)
			}
			if tt.errContains != "" && err != nil {
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errContains)
				}
			}
		})
	}
}
