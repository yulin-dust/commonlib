package lclient

import (
	"math/rand/v2"
	"net/url"
	"strings"
)

// 本文件提供「浏览器身份轮换」——一种比"随机 headers"更可靠的反爬手段。
//
// 为什么不直接随机打乱 header / 随机拼 UA？
//
//	因为在风控视角里，请求头顺序本身就是指纹的一部分；而 User-Agent 必须与
//	TLS/JA3 指纹（impersonate profile）以及 sec-ch-ua 客户端提示三者严格自洽，
//	任何一项对不上都会暴露"这是个脚本"。单独随机某个 header 只会制造矛盾、
//	更容易被识别。
//
// 正确的做法是"整套身份轮换"：维护一组完整、内部自洽的真实浏览器身份
// （TLS profile + 匹配的 UA + sec-ch-ua + 正确的 header 顺序），每次请求随机
// 选一个、整套套用。这样每个请求看起来都像一个真实且不同的浏览器。
//
// 用法：
//
//	s := lclient.NewSession(lclient.WithRandomIdentity()) // 每次请求随机一个身份
//	s.Get("https://example.com")
//
//	// 想顺带随机 Accept-Language：
//	s := lclient.NewSession(lclient.WithRandomIdentity(
//	        lclient.WithRandomAcceptLanguage(), // 用内置语言池
//	))
//
//	// 想限定只在某几个浏览器之间轮换：
//	s := lclient.NewSession(lclient.WithRandomIdentity(
//	        lclient.WithIdentityPool(lclient.ChromeIdentities()...),
//	))

// browserKind 决定 header 模板（不同内核的默认请求头集合与顺序不同）。
type browserKind int

const (
	kindChromium browserKind = iota // Chrome / Edge：带 sec-ch-ua 客户端提示
	kindFirefox                     // Firefox：无 sec-ch-ua
	kindSafari                      // Safari：无 sec-ch-ua、Sec-Fetch 较少
)

// BrowserIdentity 是一套自洽的浏览器身份。各字段彼此匹配，不应单独替换其中一项
// （例如只改 UserAgent 而不改 Profile 会破坏自洽性）。
type BrowserIdentity struct {
	Name            string      // 人类可读标识，如 "Chrome 131 / Windows"
	Profile         string      // TLS impersonate profile，对应 impersonate.go 的常量
	UserAgent       string      // 与 Profile 匹配的 UA
	SecChUa         string      // sec-ch-ua（仅 Chromium 有意义）
	SecChUaMobile   string      // sec-ch-ua-mobile，桌面为 "?0"
	SecChUaPlatform string      // sec-ch-ua-platform，如 "Windows" / "macOS"
	AcceptLanguage  string      // 默认 Accept-Language（可被随机语言池覆盖）
	kind            browserKind // 决定 header 模板
}

// identityHeaderKeys 是"身份定义型"请求头：它们必须与 Profile 自洽，因此在套用
// 身份时不允许被 session 默认 headers 悄悄覆盖（但仍允许调用方在单次请求里显式
// 覆盖——那是调用方自己的明确选择）。
var identityHeaderKeys = map[string]bool{
	"user-agent":         true,
	"sec-ch-ua":          true,
	"sec-ch-ua-mobile":   true,
	"sec-ch-ua-platform": true,
}

func isIdentityHeader(key string) bool {
	return identityHeaderKeys[strings.ToLower(key)]
}

// Headers 按该身份对应浏览器的真实顺序，构造一整套请求头。
// acceptLang 非空时覆盖该身份默认的 Accept-Language（用于语言随机化）。
//
// secure 表示目标是否安全上下文（https，或 localhost）。它影响 Chromium 的两类头：
//   - 客户端提示 sec-ch-ua / -mobile / -platform：真实 Chrome 只在安全上下文发送，
//     明文 http 不发——在 http 上仍发反而是个破绽。
//   - priority（Extensible Priorities）：只在 h2/h3 上有意义（curl 仅在 TLS 上协商
//     h2），明文 http 走 HTTP/1.1 不发。
//
// Sec-Fetch-* 与 Upgrade-Insecure-Requests 真实浏览器对 http/https 都发，故不受影响。
func (id BrowserIdentity) Headers(acceptLang string, secure bool) OrderedHeaders {
	lang := acceptLang
	if lang == "" {
		lang = id.AcceptLanguage
	}
	if lang == "" {
		lang = "en-US,en;q=0.9"
	}

	var h OrderedHeaders
	switch id.kind {
	case kindFirefox:
		h = OrderedHeaders{
			{"User-Agent", id.UserAgent},
			{"Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8"},
			{"Accept-Language", lang},
			{"Accept-Encoding", "gzip, deflate, br, zstd"},
			{"Upgrade-Insecure-Requests", "1"},
			{"Sec-Fetch-Dest", "document"},
			{"Sec-Fetch-Mode", "navigate"},
			{"Sec-Fetch-Site", "none"},
			{"Sec-Fetch-User", "?1"},
		}
	case kindSafari:
		h = OrderedHeaders{
			{"Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"},
			{"Accept-Encoding", "gzip, deflate, br"},
			{"User-Agent", id.UserAgent},
			{"Accept-Language", lang},
		}
	default: // kindChromium
		if secure {
			h = append(h,
				[2]string{"sec-ch-ua", id.SecChUa},
				[2]string{"sec-ch-ua-mobile", id.SecChUaMobile},
				[2]string{"sec-ch-ua-platform", quoteUnquoted(id.SecChUaPlatform)},
			)
		}
		h = append(h,
			[2]string{"Upgrade-Insecure-Requests", "1"},
			[2]string{"User-Agent", id.UserAgent},
			[2]string{"Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"},
			[2]string{"Sec-Fetch-Site", "none"},
			[2]string{"Sec-Fetch-Mode", "navigate"},
			[2]string{"Sec-Fetch-User", "?1"},
			[2]string{"Sec-Fetch-Dest", "document"},
			[2]string{"Accept-Encoding", "gzip, deflate, br, zstd"},
			[2]string{"Accept-Language", lang},
		)
		if secure {
			// priority 真实 Chrome 在 h2/h3 导航上发；明文 http(1.1) 不发。
			h = append(h, [2]string{"priority", "u=0, i"})
		}
	}
	return h
}

// isSecureContext 报告目标 URL 是否安全上下文：https，或浏览器同样视为可信的
// localhost / 环回地址。解析失败时按安全处理（多数目标是 https，且更贴近浏览器默认）。
func isSecureContext(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return true
	}
	if strings.EqualFold(u.Scheme, "https") {
		return true
	}
	host := u.Hostname()
	return host == "localhost" || strings.HasSuffix(host, ".localhost") ||
		host == "127.0.0.1" || host == "::1"
}

// quoteUnquoted 把 sec-ch-ua-platform 的值补上引号（Chrome 实际发送 "Windows"）。
func quoteUnquoted(s string) string {
	if s == "" {
		return s
	}
	if strings.HasPrefix(s, `"`) {
		return s
	}
	return `"` + s + `"`
}

// ============================================================
// 内置身份池
// ============================================================

// builtinIdentities 是内置的真实浏览器身份池。
//
// 不变量：每个身份的 Profile 都必须是当前 libcurl-impersonate 真正支持的 target
// （否则运行时 curl_easy_impersonate 会失败）；UA / sec-ch-ua / 平台逐项与该
// Profile 对齐，确保整套自洽。下面这些 Profile 均在 1.2.x 上实测可用。
var builtinIdentities = []BrowserIdentity{
	{
		Name:            "Chrome 131 / Windows",
		Profile:         Chrome131,
		UserAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
		SecChUa:         `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`,
		SecChUaMobile:   "?0",
		SecChUaPlatform: "Windows",
		AcceptLanguage:  "en-US,en;q=0.9",
		kind:            kindChromium,
	},
	{
		Name:            "Chrome 131 / macOS",
		Profile:         Chrome131,
		UserAgent:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
		SecChUa:         `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`,
		SecChUaMobile:   "?0",
		SecChUaPlatform: "macOS",
		AcceptLanguage:  "en-US,en;q=0.9",
		kind:            kindChromium,
	},
	{
		Name:            "Chrome 124 / Windows",
		Profile:         Chrome124,
		UserAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
		SecChUa:         `"Google Chrome";v="124", "Chromium";v="124", "Not-A.Brand";v="99"`,
		SecChUaMobile:   "?0",
		SecChUaPlatform: "Windows",
		AcceptLanguage:  "en-US,en;q=0.9",
		kind:            kindChromium,
	},
	{
		Name:            "Chrome 120 / macOS",
		Profile:         Chrome120,
		UserAgent:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		SecChUa:         `"Not_A Brand";v="8", "Chromium";v="120", "Google Chrome";v="120"`,
		SecChUaMobile:   "?0",
		SecChUaPlatform: "macOS",
		AcceptLanguage:  "en-US,en;q=0.9",
		kind:            kindChromium,
	},
	{
		Name:           "Firefox 133 / Windows",
		Profile:        Firefox133,
		UserAgent:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) Gecko/20100101 Firefox/133.0",
		AcceptLanguage: "en-US,en;q=0.5",
		kind:           kindFirefox,
	},
	{
		Name:           "Firefox 133 / macOS",
		Profile:        Firefox133,
		UserAgent:      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:133.0) Gecko/20100101 Firefox/133.0",
		AcceptLanguage: "en-US,en;q=0.5",
		kind:           kindFirefox,
	},
	{
		Name:           "Safari 18 / macOS",
		Profile:        Safari18, // = safari18_0
		UserAgent:      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15",
		AcceptLanguage: "en-US,en;q=0.9",
		kind:           kindSafari,
	},
	{
		Name:           "Safari 17 / macOS",
		Profile:        Safari17, // = safari17_0
		UserAgent:      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15",
		AcceptLanguage: "en-US,en;q=0.9",
		kind:           kindSafari,
	},
}

// DefaultIdentities 返回内置身份池的一份副本（可安全修改后传给 WithIdentityPool）。
func DefaultIdentities() []BrowserIdentity {
	out := make([]BrowserIdentity, len(builtinIdentities))
	copy(out, builtinIdentities)
	return out
}

// ChromeIdentities 仅返回 Chromium 系（Chrome/Edge）身份，适合只想伪装成 Chrome 的场景。
func ChromeIdentities() []BrowserIdentity {
	var out []BrowserIdentity
	for _, id := range builtinIdentities {
		if id.kind == kindChromium {
			out = append(out, id)
		}
	}
	return out
}

// defaultAcceptLanguages 是随机 Accept-Language 时使用的内置语言池。
var defaultAcceptLanguages = []string{
	"en-US,en;q=0.9",
	"en-US,en;q=0.9,zh-CN;q=0.8,zh;q=0.7",
	"zh-CN,zh;q=0.9,en;q=0.8",
	"en-GB,en;q=0.9",
	"en-US,en;q=0.8",
}

// ============================================================
// 轮换器
// ============================================================

// IdentityRotator 负责每次请求挑选一个身份；并发安全（math/rand/v2 的顶层函数
// 可安全并发调用）。
type IdentityRotator struct {
	pool          []BrowserIdentity
	randomizeLang bool
	localePool    []string
}

// IdentityOption 配置 WithRandomIdentity 的行为。
type IdentityOption func(*IdentityRotator)

// WithIdentityPool 指定要轮换的身份池（默认用全部内置身份）。
func WithIdentityPool(ids ...BrowserIdentity) IdentityOption {
	return func(r *IdentityRotator) {
		if len(ids) > 0 {
			r.pool = ids
		}
	}
}

// WithRandomAcceptLanguage 开启 Accept-Language 随机化。
// 不传参数时用内置语言池；也可传入自定义语言列表。
func WithRandomAcceptLanguage(langs ...string) IdentityOption {
	return func(r *IdentityRotator) {
		r.randomizeLang = true
		if len(langs) > 0 {
			r.localePool = langs
		}
	}
}

// newIdentityRotator 按选项构造轮换器。
func newIdentityRotator(opts ...IdentityOption) *IdentityRotator {
	r := &IdentityRotator{
		pool:       builtinIdentities,
		localePool: defaultAcceptLanguages,
	}
	for _, opt := range opts {
		opt(r)
	}
	if len(r.pool) == 0 {
		r.pool = builtinIdentities
	}
	return r
}

// pick 随机返回一个身份。
func (r *IdentityRotator) pick() BrowserIdentity {
	if len(r.pool) == 1 {
		return r.pool[0]
	}
	return r.pool[rand.IntN(len(r.pool))]
}

// acceptLang 返回本次要用的 Accept-Language；未开启随机化时返回 ""（即用身份默认值）。
func (r *IdentityRotator) acceptLang() string {
	if !r.randomizeLang || len(r.localePool) == 0 {
		return ""
	}
	return r.localePool[rand.IntN(len(r.localePool))]
}

// apply 把随机身份套用到 prep 上。
//
// 进入时 prep.Headers 是 session 默认 headers 的副本。套用规则：
//   - 以身份的完整有序请求头为基底（保证顺序真实）；
//   - session 默认 headers 叠加到基底之上，但绝不覆盖"身份定义型" header
//     （UA / sec-ch-ua*），以免破坏与 TLS 指纹的自洽；
//   - prep.Profile 改为身份对应的 TLS profile。
//
// 之后 Do() 里的请求级 options 仍会再叠加一层——调用方在单次请求里显式设置的
// header 始终拥有最高优先级。
func (r *IdentityRotator) apply(prep *PreparedRequest) {
	id := r.pick()
	base := id.Headers(r.acceptLang(), isSecureContext(prep.URL))
	for _, kv := range prep.Headers {
		if isIdentityHeader(kv[0]) {
			continue // 不让 session 默认值破坏身份自洽
		}
		base.Set(kv[0], kv[1])
	}
	prep.Headers = base
	prep.Profile = id.Profile
}

// ============================================================
// 请求级一次性身份覆盖
// ============================================================

type oIdentity struct {
	id   BrowserIdentity
	lang string
}

func (o oIdentity) applyToRequest(r *PreparedRequest, _ sessionSnapshot) {
	r.Profile = o.id.Profile
	for _, kv := range o.id.Headers(o.lang, isSecureContext(r.URL)) {
		r.Headers.Set(kv[0], kv[1])
	}
}

// UseIdentity 在单次请求中强制使用指定身份（覆盖 session 的 profile 与身份 header）。
// 适合"这一个请求我要伪装成特定浏览器"的场景。
func UseIdentity(id BrowserIdentity) RequestOption {
	return oIdentity{id: id}
}
