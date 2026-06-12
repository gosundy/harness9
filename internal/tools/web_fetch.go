// 内置工具：WebFetch（网页抓取工具）。
//
// 抓取指定 URL 的网页，提取主内容并返回 Markdown 格式。
// 所有请求在发出前通过 isSafeURL 校验，防止 SSRF 攻击。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/harness9/internal/schema"
)

const (
	fetchTimeout      = 15 * time.Second
	fetchMaxRedirects = 5
	webUserAgent      = "harness9/1.0"
)

// WebFetchTool 实现 BaseTool 接口，抓取网页并返回 Markdown 内容。
type WebFetchTool struct {
	// safetyCheck 默认为 isSafeURL，测试中可替换为 no-op 以访问 httptest 服务器
	safetyCheck func(string) error
	client      *http.Client
}

// NewWebFetchTool 创建生产用的 WebFetchTool（含完整 SSRF 检查）。
func NewWebFetchTool() *WebFetchTool {
	t := &WebFetchTool{safetyCheck: isSafeURL}
	t.client = &http.Client{
		Timeout: fetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= fetchMaxRedirects {
				return fmt.Errorf("exceeded max redirects (%d)", fetchMaxRedirects)
			}
			if err := t.safetyCheck(req.URL.String()); err != nil {
				return fmt.Errorf("redirect target unsafe: %w", err)
			}
			return nil
		},
	}
	return t
}

func (t *WebFetchTool) Name() string { return "web_fetch" }

func (t *WebFetchTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name: t.Name(),
		Description: "抓取指定 URL 的网页内容，返回 Markdown 格式的主要内容。" +
			"适合读取文档、博客、新闻等页面。" +
			"若需先搜索再抓取，请先使用 web_search 获取 URL。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"url": map[string]interface{}{
					"type":        "string",
					"description": "要抓取的完整 URL，必须以 http:// 或 https:// 开头",
				},
				"max_chars": map[string]interface{}{
					"type": "integer",
					"description": fmt.Sprintf(
						"返回内容的最大字符数（默认 %d，最大 %d）",
						defaultMaxChars, hardMaxChars,
					),
				},
			},
			"required": []string{"url"},
		},
	}
}

type webFetchArgs struct {
	URL      string `json:"url"`
	MaxChars int    `json:"max_chars,omitempty"`
}

func (t *WebFetchTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var input webFetchArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("parse args failed: %w", err)
	}
	if input.URL == "" {
		return "Error: url parameter is required", nil
	}

	if err := t.safetyCheck(input.URL); err != nil {
		return fmt.Sprintf("Error: URL safety check failed — %v", err), nil
	}

	maxChars := input.MaxChars
	if maxChars <= 0 {
		maxChars = defaultMaxChars
	}
	if maxChars > hardMaxChars {
		maxChars = hardMaxChars
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, input.URL, nil)
	if err != nil {
		return fmt.Sprintf("Error: create request failed — %v", err), nil
	}
	req.Header.Set("User-Agent", webUserAgent)
	req.Header.Set("Accept", "text/html,text/plain,*/*")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Sprintf("Error: request failed — %v", err), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Sprintf("Error: HTTP %d %s", resp.StatusCode, resp.Status), nil
	}

	contentType := resp.Header.Get("Content-Type")
	switch {
	case strings.Contains(contentType, "text/html"):
		return extractContent(resp.Body, input.URL, maxChars)
	case strings.HasPrefix(contentType, "text/"):
		data, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxChars)+1))
		if err != nil {
			return fmt.Sprintf("Error: read body failed — %v", err), nil
		}
		text := string(data)
		if len(text) > maxChars {
			// 回退到 UTF-8 rune 边界，防止截断多字节字符
			cut := maxChars
			for cut > 0 && text[cut]&0xC0 == 0x80 {
				cut--
			}
			return text[:cut] + fmt.Sprintf("\n\n[内容已截断，已显示前 %d 字符]", maxChars), nil
		}
		return text, nil
	default:
		return fmt.Sprintf("不支持的内容类型：%s", contentType), nil
	}
}
