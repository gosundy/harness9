package feishu

import (
	"testing"
)

// TestTruncateRunes 使用表驱动测试验证 Unicode 字符数截断边界。
func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		max   int
		want  string
	}{
		{
			name:  "短于限制，不截断",
			input: "hello",
			max:   10,
			want:  "hello",
		},
		{
			name:  "等于限制，不截断",
			input: "hello",
			max:   5,
			want:  "hello",
		},
		{
			name:  "超出限制，追加省略号",
			input: "hello world",
			max:   5,
			want:  "hello...",
		},
		{
			name:  "空字符串",
			input: "",
			max:   10,
			want:  "",
		},
		{
			name:  "中文字符按 rune 计数",
			input: "你好世界",
			max:   3,
			want:  "你好世...",
		},
		{
			name:  "多字节字符不截断字节（中英混合）",
			input: "hello你好",
			max:   5,
			want:  "hello...",
		},
		{
			name:  "max=0 截断全部，仅省略号",
			input: "abc",
			max:   0,
			want:  "...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateRunes(tt.input, tt.max)
			if got != tt.want {
				t.Errorf("truncateRunes(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
			}
		})
	}
}

// TestBuildTextContent 验证 buildTextContent 正确序列化飞书文本消息格式。
func TestBuildTextContent(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "普通文本",
			input: "hello world",
			want:  `{"text":"hello world"}`,
		},
		{
			name:  "空文本",
			input: "",
			want:  `{"text":""}`,
		},
		{
			name:  "包含双引号的文本（需转义）",
			input: `say "hello"`,
			want:  `{"text":"say \"hello\""}`,
		},
		{
			name:  "包含中文",
			input: "你好，世界！",
			want:  `{"text":"你好，世界！"}`,
		},
		{
			name:  "包含换行符",
			input: "line1\nline2",
			want:  `{"text":"line1\nline2"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildTextContent(tt.input)
			if got != tt.want {
				t.Errorf("buildTextContent(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestDerefStr 验证安全解引用 *string 的边界行为。
func TestDerefStr(t *testing.T) {
	t.Run("nil 指针返回空字符串", func(t *testing.T) {
		got := derefStr(nil)
		if got != "" {
			t.Errorf("derefStr(nil) = %q, want empty string", got)
		}
	})

	t.Run("非 nil 指针返回原始值", func(t *testing.T) {
		s := "hello"
		got := derefStr(&s)
		if got != s {
			t.Errorf("derefStr(&%q) = %q, want %q", s, got, s)
		}
	})

	t.Run("空字符串指针返回空字符串", func(t *testing.T) {
		s := ""
		got := derefStr(&s)
		if got != "" {
			t.Errorf("derefStr(&empty) = %q, want empty string", got)
		}
	})
}
