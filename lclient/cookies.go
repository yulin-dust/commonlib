package lclient

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// CookieJar 是线程安全的 cookie 容器，按 domain + path 分组。
// 完全自实现，不依赖 net/http/cookiejar（后者与 curl 的 cookie 行为略有差异）。
type CookieJar struct {
	mu      sync.RWMutex
	cookies map[string][]*http.Cookie // key: domain
}

// NewCookieJar 创建一个空 jar。
func NewCookieJar() *CookieJar {
	return &CookieJar{cookies: make(map[string][]*http.Cookie)}
}

// SetCookies 为给定 URL 存储 cookies（同 net/http/cookiejar 接口）。
//
// 安全：服务器通过 Set-Cookie 的 Domain 属性声明的作用域，必须覆盖当前请求
// 的 host（即 host 等于该 domain 或是其子域），且不能是公共后缀（如 "com"）。
// 否则丢弃，防止 evil.com 给 bank.com / 顶级域写 cookie 的跨域注入。
//
// host-only 语义：没带 Domain 属性的 cookie 只属于下发它的那个 host，不该发给
// 子域（RFC 6265 §5.3）。存储上沿用 Netscape / curl cookie jar 的老约定——带
// Domain 的存成 ".example.com"（前导点 = 含子域），host-only 的存成
// "example.com"（无点）——这样导出的 []*http.Cookie 仍是标准类型，JSON 持久化
// 也能原样往返。
func (j *CookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	if u == nil {
		return
	}
	host := canonicalHost(u.Host)
	j.mu.Lock()
	defer j.mu.Unlock()

	for _, c := range cookies {
		c2 := *c
		if c2.Path == "" {
			c2.Path = "/"
		}

		// 计算并校验作用域 domain
		domain := host
		hostOnly := true
		if c2.Domain != "" {
			cd := canonicalHost(strings.TrimPrefix(c2.Domain, "."))
			if cd != host {
				// 显式 Domain 想扩大作用域：必须是 host 的父域，
				// 且不能是单标签公共后缀。
				if !domainMatch(host, cd) || !strings.Contains(cd, ".") {
					continue // 非法 Domain，丢弃
				}
			}
			domain = cd
			hostOnly = false // 显式 Domain → 含子域
		}
		// 桶 key 始终是不带点的规范 domain；点只写进 c2.Domain 作为标记。
		if hostOnly {
			c2.Domain = domain
		} else {
			c2.Domain = "." + domain
		}

		j.upsertLocked(domain, &c2)
	}
}

// upsertLocked 在已加锁状态下，把 cookie 按 (Name, Path) 覆盖写入 domain 桶。
func (j *CookieJar) upsertLocked(domain string, c *http.Cookie) {
	existing := j.cookies[domain]
	for i, e := range existing {
		if e.Name == c.Name && e.Path == c.Path {
			existing[i] = c
			j.cookies[domain] = existing
			return
		}
	}
	j.cookies[domain] = append(existing, c)
}

// Cookies 取出 URL 应该携带的 cookies。
func (j *CookieJar) Cookies(u *url.URL) []*http.Cookie {
	if u == nil {
		return nil
	}
	j.mu.RLock()
	defer j.mu.RUnlock()

	host := canonicalHost(u.Host)
	now := time.Now()
	var out []*http.Cookie

	// 只查 host 自身及其各级父域对应的桶，避免遍历整个 jar。
	for _, domain := range hostDomainKeys(host) {
		// 父域桶里只有"带 Domain 属性"的 cookie 才该发过来；
		// 该父域自己下发的 host-only cookie 不属于当前子域。
		parentBucket := domain != host
		for _, c := range j.cookies[domain] {
			// 过期判断
			if !c.Expires.IsZero() && c.Expires.Before(now) {
				continue
			}
			if parentBucket && !strings.HasPrefix(c.Domain, ".") {
				continue // host-only，不外溢到子域
			}
			// 路径判断（RFC 6265 §5.1.4）
			if !pathMatch(u.Path, c.Path) {
				continue
			}
			// Secure 判断
			if c.Secure && u.Scheme != "https" {
				continue
			}
			out = append(out, c)
		}
	}
	return out
}

// pathMatch 实现 RFC 6265 §5.1.4 的 path-match：要么完全相等，要么 cookiePath
// 是 reqPath 的前缀且边界落在 "/" 上。纯 strings.HasPrefix 会让 Path=/ab 的
// cookie 错误地发给 /abc 这种无关路径。
func pathMatch(reqPath, cookiePath string) bool {
	if cookiePath == "" || cookiePath == "/" {
		return true
	}
	if reqPath == "" {
		reqPath = "/"
	}
	if reqPath == cookiePath {
		return true
	}
	if !strings.HasPrefix(reqPath, cookiePath) {
		return false
	}
	// /foo 匹配 /foo/bar；/foo/ 也匹配 /foo/bar
	return strings.HasSuffix(cookiePath, "/") || reqPath[len(cookiePath)] == '/'
}

// hostDomainKeys 返回 host 自身及其逐级父域，用于在按 domain 分桶的 jar 中
// 直接定位候选桶（O(标签数)），替代对所有 domain 的线性扫描。
// 例如 "a.b.example.com" → ["a.b.example.com", "b.example.com", "example.com", "com"]。
func hostDomainKeys(host string) []string {
	host = canonicalHost(host)
	if host == "" {
		return nil
	}
	keys := []string{host}
	for i := 0; i < len(host); i++ {
		if host[i] == '.' {
			keys = append(keys, host[i+1:])
		}
	}
	return keys
}

// CookiesAsHeader 返回 "k1=v1; k2=v2" 格式，用于直接拼到 Cookie header。
func (j *CookieJar) CookiesAsHeader(u *url.URL) string {
	cs := j.Cookies(u)
	if len(cs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

// Clear 清空 jar。
func (j *CookieJar) Clear() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.cookies = make(map[string][]*http.Cookie)
}

// ClearDomain 清空指定 domain 的 cookies。
func (j *CookieJar) ClearDomain(domain string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	delete(j.cookies, canonicalHost(strings.TrimPrefix(domain, ".")))
}

// ExportByDomain 导出某 domain 的全部 cookies（用于持久化）。
func (j *CookieJar) ExportByDomain(domain string) []*http.Cookie {
	j.mu.RLock()
	defer j.mu.RUnlock()
	src := j.cookies[canonicalHost(strings.TrimPrefix(domain, "."))]
	out := make([]*http.Cookie, len(src))
	for i, c := range src {
		cc := *c
		out[i] = &cc
	}
	return out
}

// ExportAll 导出所有 cookies。
func (j *CookieJar) ExportAll() map[string][]*http.Cookie {
	j.mu.RLock()
	defer j.mu.RUnlock()
	out := make(map[string][]*http.Cookie, len(j.cookies))
	for d, list := range j.cookies {
		clone := make([]*http.Cookie, len(list))
		for i, c := range list {
			cc := *c
			clone[i] = &cc
		}
		out[d] = clone
	}
	return out
}

// SaveToFile 把所有 cookies 保存到 JSON 文件。
func (j *CookieJar) SaveToFile(path string) error {
	data := j.ExportAll()
	bs, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, bs, 0600)
}

// LoadFromFile 从 JSON 文件加载 cookies（追加，不覆盖现有）。
func (j *CookieJar) LoadFromFile(path string) error {
	bs, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var data map[string][]*http.Cookie
	if err := json.Unmarshal(bs, &data); err != nil {
		return fmt.Errorf("parse cookie file: %w", err)
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for d, list := range data {
		for _, c := range list {
			if c == nil {
				continue
			}
			// 从 cookie 自身 Domain 重新推导桶 key，兼容旧文件里带前导点
			// 或 key 与 Domain 不一致的情况；同时按 (Name,Path) 去重覆盖，
			// 避免重复 Load 导致 cookie 无限累积。
			domain := canonicalHost(strings.TrimPrefix(c.Domain, "."))
			if domain == "" {
				domain = canonicalHost(strings.TrimPrefix(d, "."))
			}
			j.upsertLocked(domain, c)
		}
	}
	return nil
}

// canonicalHost 把 host 标准化（去端口、转小写、剥掉 IPv6 字面量的方括号）。
// 直接按第一个 ":" 截断会把 "[::1]:8080" 切成 "["，故 IPv6 要单独处理。
func canonicalHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if strings.HasPrefix(h, "[") {
		if i := strings.Index(h, "]"); i >= 0 {
			return h[1:i] // "[::1]:8080" → "::1"
		}
		return h
	}
	// 仅在恰好有一个 ":" 时视为 host:port；多个 ":" 说明是没加括号的裸 IPv6。
	if i := strings.Index(h, ":"); i >= 0 && strings.Count(h, ":") == 1 {
		h = h[:i]
	}
	return h
}

// domainMatch 判断 host 是否落在 cookieDomain 范围内。
func domainMatch(host, cookieDomain string) bool {
	host = canonicalHost(host)
	cookieDomain = canonicalHost(cookieDomain)
	cookieDomain = strings.TrimPrefix(cookieDomain, ".")
	if host == cookieDomain {
		return true
	}
	if strings.HasSuffix(host, "."+cookieDomain) {
		return true
	}
	return false
}
