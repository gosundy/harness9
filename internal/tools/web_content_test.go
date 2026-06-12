package tools

import (
	"strings"
	"testing"
)

func TestExtractContentBasic(t *testing.T) {
	htmlBody := `<!DOCTYPE html>
<html>
<head><title>Test Article</title></head>
<body>
<article>
<h1>Hello World</h1>
<p>This is a <strong>bold</strong> paragraph.</p>
<p>Second paragraph with a <a href="https://example.com">link</a>.</p>
</article>
</body>
</html>`

	result, err := extractContent(strings.NewReader(htmlBody), "https://example.com/article", defaultMaxChars)
	if err != nil {
		t.Fatalf("extractContent returned error: %v", err)
	}
	if !strings.Contains(result, "来源：https://example.com/article") {
		t.Errorf("result should contain source URL\ngot:\n%s", result)
	}
	if !strings.Contains(result, "Hello World") {
		t.Errorf("result should contain heading\ngot:\n%s", result)
	}
	if !strings.Contains(result, "bold") {
		t.Errorf("result should contain bold text\ngot:\n%s", result)
	}
}

func TestExtractContentTruncation(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("<html><body><article>")
	for i := 0; i < 500; i++ {
		sb.WriteString("<p>This is a sufficiently long paragraph to ensure content exceeds limit.</p>")
	}
	sb.WriteString("</article></body></html>")

	result, err := extractContent(strings.NewReader(sb.String()), "https://example.com/long", 200)
	if err != nil {
		t.Fatalf("extractContent error: %v", err)
	}
	if len(result) > 400 {
		t.Errorf("result should be truncated, got %d chars", len(result))
	}
	if !strings.Contains(result, "已截断") {
		t.Errorf("result should contain truncation marker\ngot:\n%s", result)
	}
}

func TestExtractPlainText(t *testing.T) {
	htmlBody := `<html><body>
<script>alert("should be hidden")</script>
<style>.cls { color: red }</style>
<p>Visible paragraph one.</p>
<p>Visible paragraph two.</p>
</body></html>`

	result := extractPlainText(strings.NewReader(htmlBody))
	if strings.Contains(result, "alert") {
		t.Errorf("plain text should not contain script content\ngot: %s", result)
	}
	if strings.Contains(result, ".cls") {
		t.Errorf("plain text should not contain style content\ngot: %s", result)
	}
	if !strings.Contains(result, "Visible paragraph one") {
		t.Errorf("plain text should contain visible text\ngot: %s", result)
	}
}

func TestExtractContentOversizedBody(t *testing.T) {
	big := strings.Repeat("a", maxHTMLBodySize+1)
	_, err := extractContent(strings.NewReader("<html><body>"+big+"</body></html>"), "https://example.com", defaultMaxChars)
	if err == nil {
		t.Error("expected error for oversized body, got nil")
	}
}

func TestAssemblePageUTF8SafeTruncation(t *testing.T) {
	// 用中文内容填充超出 maxChars 的页面，验证截断点不会切断多字节 UTF-8 字符。
	content := strings.Repeat("你好世界！", 1000) // 每个 "你好世界！" = 15 字节（UTF-8）
	result := assemblePage("https://example.com", "", content, 100)

	// 截断标记必须存在
	if !strings.Contains(result, "已截断") {
		t.Errorf("result should contain truncation marker\ngot:\n%s", result)
	}
	// 结果必须是合法 UTF-8
	if !isValidUTF8(result) {
		t.Errorf("truncated result is not valid UTF-8")
	}
}

// isValidUTF8 检查字符串是否是合法 UTF-8 序列。
func isValidUTF8(s string) bool {
	for i := 0; i < len(s); {
		b := s[i]
		var size int
		switch {
		case b < 0x80:
			size = 1
		case b < 0xC0:
			return false // 非法起始字节（continuation byte）
		case b < 0xE0:
			size = 2
		case b < 0xF0:
			size = 3
		default:
			size = 4
		}
		if i+size > len(s) {
			return false
		}
		for j := 1; j < size; j++ {
			if s[i+j]&0xC0 != 0x80 {
				return false
			}
		}
		i += size
	}
	return true
}
