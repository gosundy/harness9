// SSRF 防护工具（URL 安全校验）。
//
// isSafeURL 是 web_fetch 和 web_search 的共享安全门，
// 类比 safe_path.go 对文件系统的保护——在发出任何 HTTP 请求前调用。
package tools

import (
	"fmt"
	"net"
	"net/url"
)

// blockedCIDRs 是永远拒绝访问的 IP 网段。
// 169.254.0.0/16 永久禁止（云厂商 metadata 端点），其余为默认禁止的私网地址。
var blockedCIDRs = func() []*net.IPNet {
	cidrs := []string{
		"169.254.0.0/16", // 链路本地 + AWS/Azure/GCP metadata
		"127.0.0.0/8",    // IPv4 loopback
		"10.0.0.0/8",     // RFC1918
		"172.16.0.0/12",  // RFC1918
		"192.168.0.0/16", // RFC1918
		"100.64.0.0/10",  // CGNAT
	}
	var nets []*net.IPNet
	for _, cidr := range cidrs {
		_, n, err := net.ParseCIDR(cidr)
		if err == nil {
			nets = append(nets, n)
		}
	}
	return nets
}()

// isSafeURL 校验 rawURL 是否允许发起 HTTP 请求。
//
// 检查链：scheme → userinfo → DNS 解析 → IP 段检查。
// DNS 解析失败时 fail-closed（拒绝请求），防止 DNS rebinding 攻击。
func isSafeURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("无效 URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme: %s（仅允许 http/https）", u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("URL 中包含 userinfo（user:pass@...），拒绝访问")
	}

	host := u.Hostname()
	addrs, err := net.LookupHost(host)
	if err != nil {
		// DNS 解析失败 → fail-closed
		return fmt.Errorf("DNS 解析失败（%s）: %w", host, err)
	}

	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			continue
		}
		// 规范化 IPv4-mapped IPv6（如 ::ffff:169.254.x.x）
		if ip4 := ip.To4(); ip4 != nil {
			ip = ip4
		}
		// IPv6 loopback
		if ip.Equal(net.IPv6loopback) {
			return fmt.Errorf("blocked: loopback 地址 %s", ip)
		}
		for _, network := range blockedCIDRs {
			if network.Contains(ip) {
				return fmt.Errorf("blocked: 私有/保留地址 %s（范围 %s）", ip, network)
			}
		}
	}
	return nil
}
