package lclient

import (
	"strings"
	"testing"
)

// 每个内置身份都应内部自洽：profile 非空、UA 与内核匹配、Chromium 必须带 sec-ch-ua。
func TestBuiltinIdentitiesCoherent(t *testing.T) {
	for _, id := range builtinIdentities {
		if id.Profile == "" || id.UserAgent == "" {
			t.Errorf("%s: 缺少 Profile 或 UserAgent", id.Name)
		}
		switch id.kind {
		case kindChromium:
			if id.SecChUa == "" {
				t.Errorf("%s: Chromium 身份缺少 sec-ch-ua", id.Name)
			}
			if !strings.Contains(id.UserAgent, "Chrome") {
				t.Errorf("%s: Chromium UA 里应含 Chrome", id.Name)
			}
		case kindFirefox:
			if !strings.Contains(id.UserAgent, "Firefox") {
				t.Errorf("%s: Firefox UA 里应含 Firefox", id.Name)
			}
		}
	}
}

// Headers 应按内核给出正确的头集合：Chromium 带 sec-ch-ua，Firefox 不带。
func TestIdentityHeaders(t *testing.T) {
	chrome := builtinIdentities[0] // Chrome 131 / Windows
	h := chrome.Headers("", true)  // 安全上下文（https）
	if h.Get("User-Agent") != chrome.UserAgent {
		t.Error("Chrome headers 的 UA 不匹配")
	}
	if h.Get("sec-ch-ua") == "" {
		t.Error("https 下 Chrome headers 应带 sec-ch-ua")
	}
	if h.Get("priority") == "" {
		t.Error("https 下 Chrome headers 应带 priority")
	}
	if h.Get("sec-ch-ua-platform") != `"Windows"` {
		t.Errorf("sec-ch-ua-platform 应为带引号的 \"Windows\"，实际 %q", h.Get("sec-ch-ua-platform"))
	}
	// sec-ch-ua 应排在 User-Agent 之前（真实 Chrome 顺序）。
	keys, _ := h.Split()
	idxUA, idxCH := indexOf(keys, "User-Agent"), indexOf(keys, "sec-ch-ua")
	if idxCH < 0 || idxUA < 0 || idxCH > idxUA {
		t.Errorf("header 顺序不真实：sec-ch-ua(%d) 应在 User-Agent(%d) 之前", idxCH, idxUA)
	}

	var ff BrowserIdentity
	for _, id := range builtinIdentities {
		if id.kind == kindFirefox {
			ff = id
			break
		}
	}
	if ff.Headers("", true).Has("sec-ch-ua") {
		t.Error("Firefox headers 不应带 sec-ch-ua")
	}
}

// 明文 http 上下文：Chrome 不应发送客户端提示 sec-ch-ua* 与 priority，
// 但 Sec-Fetch-* / UA / Accept 仍应保留。
func TestIdentityHeadersInsecureContext(t *testing.T) {
	chrome := builtinIdentities[0] // Chrome 131 / Windows
	h := chrome.Headers("", false) // 非安全上下文（http）
	for _, k := range []string{"sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "priority"} {
		if h.Has(k) {
			t.Errorf("http 下不应发送 %q", k)
		}
	}
	if h.Get("User-Agent") == "" || h.Get("Sec-Fetch-Mode") == "" || h.Get("Accept") == "" {
		t.Error("http 下 UA / Sec-Fetch-* / Accept 仍应保留")
	}
}

// isSecureContext：https 与 localhost 视为安全，普通 http 视为非安全。
func TestIsSecureContext(t *testing.T) {
	secure := []string{"https://example.com", "http://localhost:8080", "http://127.0.0.1/x", "http://a.localhost"}
	insecure := []string{"http://example.com", "http://10.0.0.1", "http://example.com:80/p"}
	for _, u := range secure {
		if !isSecureContext(u) {
			t.Errorf("%s 应判为安全", u)
		}
	}
	for _, u := range insecure {
		if isSecureContext(u) {
			t.Errorf("%s 应判为非安全", u)
		}
	}
}

// 自定义 Accept-Language 应覆盖身份默认值。
func TestIdentityAcceptLanguageOverride(t *testing.T) {
	id := builtinIdentities[0]
	if got := id.Headers("fr-FR,fr;q=0.9", true).Get("Accept-Language"); got != "fr-FR,fr;q=0.9" {
		t.Errorf("Accept-Language 覆盖失败，得到 %q", got)
	}
}

// 套用身份后：profile 改为身份的 profile；session 默认的 UA 不得覆盖身份 UA；
// 但 session 默认里的其它 header（如 Referer）应保留。
func TestRotatorApplyKeepsCoherence(t *testing.T) {
	r := newIdentityRotator(WithIdentityPool(builtinIdentities[0])) // 固定 Chrome 131 Win
	prep := &PreparedRequest{
		Profile: "firefox133",
		URL:     "https://example.com", // 安全上下文，应带客户端提示
		Headers: OrderedHeaders{
			{"User-Agent", "evil-bot/1.0"}, // session 默认的"坏" UA，应被身份覆盖
			{"Referer", "https://ref.example"},
		},
	}
	r.apply(prep)

	if prep.Profile != Chrome131 {
		t.Errorf("apply 后 profile 应为 %s，实际 %s", Chrome131, prep.Profile)
	}
	if ua := prep.Headers.Get("User-Agent"); ua == "evil-bot/1.0" {
		t.Error("身份定义型 header(UA) 被 session 默认值破坏了自洽性")
	}
	if !strings.Contains(prep.Headers.Get("User-Agent"), "Chrome/131") {
		t.Errorf("UA 应为身份的 Chrome 131，实际 %q", prep.Headers.Get("User-Agent"))
	}
	if prep.Headers.Get("Referer") != "https://ref.example" {
		t.Error("session 默认的非身份 header(Referer) 应被保留")
	}
	if prep.Headers.Get("sec-ch-ua") == "" {
		t.Error("套用 Chrome 身份后应有 sec-ch-ua")
	}
}

// 随机化语言时，结果应来自语言池。
func TestRotatorRandomAcceptLanguage(t *testing.T) {
	pool := []string{"xx-XX"}
	r := newIdentityRotator(WithRandomAcceptLanguage(pool...))
	if got := r.acceptLang(); got != "xx-XX" {
		t.Errorf("随机语言应取自语言池，得到 %q", got)
	}
	// 未开启随机化时应返回空（表示用身份默认值）。
	r2 := newIdentityRotator()
	if got := r2.acceptLang(); got != "" {
		t.Errorf("未开启随机化时应返回空，得到 %q", got)
	}
}

// 轮换应能覆盖到池中多个身份（统计意义上）。
func TestRotatorPicksVariety(t *testing.T) {
	r := newIdentityRotator()
	seen := map[string]bool{}
	for i := 0; i < 300; i++ {
		seen[r.pick().Name] = true
	}
	if len(seen) < 2 {
		t.Errorf("300 次轮换只覆盖到 %d 个身份，随机性可疑", len(seen))
	}
}

func indexOf(ss []string, target string) int {
	for i, s := range ss {
		if strings.EqualFold(s, target) {
			return i
		}
	}
	return -1
}
