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
// 169.254.0.0/16 永久禁止（云厂商 metadata 端点），其余为默认禁止的私网和保留地址。
// IPv6 私有地址也列入阻止范围：
//   - fe80::/10 — IPv6 链路本地地址（Link-Local），对应 IPv4 的 169.254.0.0/16
//   - fc00::/7  — IPv6 唯一本地地址（Unique Local Address，ULA），对应 IPv4 的 RFC1918
var blockedCIDRs = func() []*net.IPNet {
	cidrs := []string{
		"169.254.0.0/16", // 链路本地 + AWS/Azure/GCP metadata
		"127.0.0.0/8",    // IPv4 loopback
		"10.0.0.0/8",     // RFC1918
		"172.16.0.0/12",  // RFC1918
		"192.168.0.0/16", // RFC1918
		"100.64.0.0/10",  // CGNAT
		"fe80::/10",      // IPv6 链路本地（Link-Local），等价于 IPv4 169.254.0.0/16
		"fc00::/7",       // IPv6 唯一本地地址（ULA），等价于 IPv4 RFC1918 私网
	}
	var nets []*net.IPNet
	for _, cidr := range cidrs {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(fmt.Sprintf("invalid CIDR %q: %v", cidr, err))
		}
		nets = append(nets, n)
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
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q, only http/https allowed", u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("URL userinfo (user:pass@host) not allowed")
	}

	host := u.Hostname()
	addrs, err := net.LookupHost(host)
	if err != nil {
		// DNS 解析失败 → fail-closed
		return fmt.Errorf("DNS lookup failed for %s: %w", host, err)
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
			return fmt.Errorf("blocked: loopback address %s", ip)
		}
		for _, network := range blockedCIDRs {
			if network.Contains(ip) {
				return fmt.Errorf("blocked: private/reserved address %s (range %s)", ip, network)
			}
		}
	}
	return nil
}
