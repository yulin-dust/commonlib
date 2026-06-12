package lclient

import (
	"context"
	"sync"
	"time"

	curl "github.com/yulin-dust/commonlib/third_party/curlimpersonate"
)

// Session 是核心客户端，类似 Python requests.Session。
// 一个 Session 应被复用，承载 cookie jar、默认 headers、默认代理等。
// Session 是并发安全的。
type Session struct {
	mu sync.RWMutex

	// 配置
	impersonate     string
	timeout         time.Duration
	proxy           string
	followRedirects bool
	maxRedirects    int
	defaultHeaders  OrderedHeaders
	autoUA          bool
	insecureTLS     bool  // true 时跳过目标 TLS 证书校验（默认 false=校验）
	maxBodyBytes    int64 // 响应体大小上限（字节），0 = 不限制
	blockPrivateIPs bool  // true 时拦截私网/环回/链路本地地址（SSRF 防护）
	autoDecode      bool  // true 时自动把响应体按字符集转成 UTF-8（GB2312/Big5 等）

	// 子模块
	jar         *CookieJar
	retry       *RetryPolicy
	limiter     *rateLimiter
	debug       *DebugLogger
	beforeHooks []BeforeRequestHook
	afterHooks  []AfterResponseHook

	// pool 非 nil 时启用连接复用（构造期一次性设定，之后只读，无需加锁）。
	pool *handlePool

	// share 非 nil 时跨句柄共享 DNS / TLS 会话缓存（线程安全，构造期设定后只读）。
	share *curl.Share

	// 上下文（取消整个 session）
	ctx    context.Context
	cancel context.CancelFunc

	manualRedirects bool // 新增

	// identity 非空时，每次请求随机套用一个自洽的浏览器身份（profile + headers）。
	identity *IdentityRotator
}

// ============================================================
// 选项
// ============================================================

// Option 是 Session 构造选项。
type Option func(*Session)

// WithImpersonate 设置浏览器指纹 profile，例如 "chrome131"。
func WithImpersonate(profile string) Option {
	return func(s *Session) { s.impersonate = profile }
}

// WithTimeout 设置请求超时（session 级，单次可覆盖）。
func WithTimeout(d time.Duration) Option {
	return func(s *Session) { s.timeout = d }
}

// WithProxy 设置代理 URL。支持：
//   - http://user:pass@host:port
//   - https://user:pass@host:port  （会自动改写为 http:// —— 见下方）
//   - socks5://user:pass@host:port
//   - socks5h://...（DNS 通过代理解析，反爬场景推荐）
//
// ⚠ https:// 代理处理：
//
//	本地 fork 已关闭代理证书校验（PROXY_SSL_VERIFYPEER/VERIFYHOST=0），https://
//	代理可直接使用。包内仍会把 https:// 代理 URL 兜底改写为 http://，对目标站点
//	HTTPS 通信无影响（CONNECT 隧道内端到端 TLS 不变）。
func WithProxy(proxy string) Option {
	if proxy == "" {
		return func(s *Session) {}
	}
	return func(s *Session) { s.proxy = proxy }
}

// WithFollowRedirects 控制是否跟随 3xx 重定向。
func WithFollowRedirects(follow bool) Option {
	return func(s *Session) { s.followRedirects = follow }
}

// WithMaxRedirects 设置最大重定向次数。
func WithMaxRedirects(n int) Option {
	return func(s *Session) { s.maxRedirects = n }
}

// WithDefaultHeaders 设置会话默认 headers（保序）。
// 风控站强烈建议显式提供与浏览器一致的 header 顺序。
func WithDefaultHeaders(h OrderedHeaders) Option {
	return func(s *Session) { s.defaultHeaders = h.Clone() }
}

// WithDefaultHeadersMap 设置会话默认 headers（无序）。
// 不保证顺序，只适合普通场景。
func WithDefaultHeadersMap(h Headers) Option {
	return func(s *Session) {
		var oh OrderedHeaders
		for k, v := range h {
			oh = append(oh, [2]string{k, v})
		}
		s.defaultHeaders = oh
	}
}

// WithAutoUserAgent 是否在未显式设置 UA 时自动按 impersonate profile 注入匹配 UA。
// 默认开启。
func WithAutoUserAgent(on bool) Option {
	return func(s *Session) { s.autoUA = on }
}

// WithAutoDecode 控制是否自动把每个响应的 body 按页面字符集转成 UTF-8（GB2312/GBK/
// Big5/Shift-JIS 等）。**默认开启**，故 resp.Text() / resp.Body 默认就是 UTF-8，
// 无需额外处理。已是 UTF-8 或检测失败时原样返回，对 UTF-8 站点无副作用。
//
// 用 WithAutoDecode(false) 关闭——典型场景是「把 body 交给会自行检测字符集的解析器
// （如 webextract）」：此时应关闭本选项、传原始 Body，让下游自己处理字符集，否则
// body 已是 UTF-8 但页面里的 <meta charset> 仍是旧值，会被二次解码而损坏内容。
func WithAutoDecode(on bool) Option {
	return func(s *Session) { s.autoDecode = on }
}

// WithBlockPrivateIPs 启用 SSRF 防护：当目标域名解析到私网 / 环回 / 链路本地
// 地址（如 127.0.0.1、10/8、192.168/16、169.254.169.254 云元数据、::1 等）时，
// 在连接后、发请求前中止，返回的错误可用 errors.Is(err, ErrBlockedAddress) 判定。
//
// 拦截发生在 IP 已解析之后，因此能挡住 DNS rebinding，也能挡住经由重定向跳到内网
// 的情况。爬取不可信 URL 时强烈建议开启。默认关闭。
//
// 限制：该检查基于"实际连接到的 IP"，因此只对直连有效。当请求经由代理（显式
// WithProxy 或命中环境变量 http_proxy/https_proxy/all_proxy，且不在 no_proxy 中）
// 时会自动跳过——此时 curl 连的是代理、由代理解析目标，连接 IP 是代理地址，按它
// 判定既会误杀也保护不到目标。经代理的出口管控应在代理侧做。
func WithBlockPrivateIPs(on bool) Option {
	return func(s *Session) { s.blockPrivateIPs = on }
}

// WithSharedCache 启用跨句柄共享的 DNS 缓存与 TLS 会话缓存。
//
// 与连接池互补：连接池复用的是"同一句柄上的活动连接"；共享缓存让所有句柄（乃至
// 无连接池时每请求新建的临时句柄）共用一份 DNS 解析结果与 TLS 会话票据——DNS 不必
// 重复解析，新建连接也能走 TLS resumption，握手更快。
//
// 共享缓存内部自带按类型分桶的锁，可被多 goroutine 并发使用。若 NewShare 失败
// （极少见），该选项静默降级为不共享。Session.Close() 会释放它。
//
// 推荐与 WithConnectionPool 搭配，对同一批站点高频抓取收益最大。
func WithSharedCache() Option {
	return func(s *Session) {
		if sh, err := curl.NewShare(); err == nil {
			s.share = sh
		}
	}
}

// WithConnectionPool 启用连接复用：复用底层 curl 句柄，保留连接 / TLS 会话 /
// DNS 缓存，省掉重复的 TCP + TLS 握手。maxIdle 是保留的空闲句柄上限（也即稳态
// 空闲连接数上限）；<=0 视为 1。默认关闭（每次请求新建一次性句柄）。
//
// 适合对同一批站点高频抓取的场景。注意：
//   - 与 WithRandomIdentity 同用时，不同身份的 TLS 参数不同，curl 可能不复用为
//     另一身份建立的连接，复用率会下降（但不影响正确性）。要最大化复用就固定身份。
//   - Session 用完应调用 Close() 释放池中句柄。
func WithConnectionPool(maxIdle int) Option {
	return func(s *Session) { s.pool = newHandlePool(maxIdle) }
}

// WithMaxResponseBytes 限制单次响应体的最大字节数，0 表示不限制（默认）。
//
// 这是给爬虫的一道安全闸：防止解压炸弹（一个几 KB 的 gzip 解出几 GB）或误抓
// 超大资源把内存吃满。超限时传输会被中止，返回的错误可用 errors.Is(err,
// ErrBodyTooLarge) 判定。限制按"解压后"的字节计（底层在写入回调里计数）。
func WithMaxResponseBytes(n int64) Option {
	return func(s *Session) {
		if n < 0 {
			n = 0
		}
		s.maxBodyBytes = n
	}
}

// WithInsecureSkipVerify 跳过目标站点 TLS 证书校验。
//
// ⚠ 安全：默认是**校验**证书（on=false）。开启后链路上的中间人（包括你用的
// 第三方代理）可解密 / 篡改 HTTPS 内容。仅在确实需要（如自签测试环境）时开启。
func WithInsecureSkipVerify(on bool) Option {
	return func(s *Session) { s.insecureTLS = on }
}

// WithCookieJar 替换 cookie jar（用于跨 session 共享或自定义）。
func WithCookieJar(jar *CookieJar) Option {
	return func(s *Session) { s.jar = jar }
}

// WithRetry 启用重试策略。
func WithRetry(p *RetryPolicy) Option {
	return func(s *Session) { s.retry = p }
}

// WithRateLimit 启用 QPS 限流。
//
//	qps: 每秒请求数；burst: 桶大小。
func WithRateLimit(qps float64, burst int) Option {
	return func(s *Session) { s.limiter = newRateLimiter(qps, burst) }
}

// WithDebug 启用 debug 日志。
func WithDebug(on bool) Option {
	return func(s *Session) {
		if s.debug == nil {
			s.debug = NewDebugLogger()
		}
		s.debug.Enable(on)
	}
}

// WithBeforeRequest 注册请求前 hook（可多个，按注册顺序执行）。
func WithBeforeRequest(h BeforeRequestHook) Option {
	return func(s *Session) { s.beforeHooks = append(s.beforeHooks, h) }
}

// WithAfterResponse 注册响应后 hook。
func WithAfterResponse(h AfterResponseHook) Option {
	return func(s *Session) { s.afterHooks = append(s.afterHooks, h) }
}

// ============================================================
// 构造
// ============================================================

// NewSession 创建一个新的会话。
func NewSession(opts ...Option) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{
		impersonate:     ChromeLatest,
		timeout:         30 * time.Second,
		followRedirects: true,
		maxRedirects:    10,
		autoUA:          true,
		autoDecode:      true, // 默认开启：自动把 GB2312/GBK/Big5 等转 UTF-8
		jar:             NewCookieJar(),
		debug:           NewDebugLogger(),
		ctx:             ctx,
		cancel:          cancel,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ============================================================
// 访问器 / 修改器（线程安全）
// ============================================================

// Jar 返回 cookie jar。
func (s *Session) Jar() *CookieJar { return s.jar }

// Debug 返回 debug logger，可继续调它的方法做精细控制。
func (s *Session) Debug() *DebugLogger { return s.debug }

// WithRandomIdentity 开启浏览器身份轮换：每次请求随机选用一套自洽的浏览器身份
// （TLS impersonate profile + 匹配的 UA / sec-ch-ua / 请求头顺序）。
//
// 这是比"随机 headers"更可靠的反爬手段——见 identity.go 的说明。默认在全部
// 内置身份间轮换；可用 WithIdentityPool / WithRandomAcceptLanguage 细化。
//
// 注意：开启后每次请求的 profile 与身份 header 由轮换器决定，会覆盖
// WithImpersonate 设的 session 级 profile（但单次请求里显式传入的 header /
// Impersonate(...) 仍优先）。
func WithRandomIdentity(opts ...IdentityOption) Option {
	return func(s *Session) { s.identity = newIdentityRotator(opts...) }
}

// WithManualRedirects 由 lclient 接管重定向（而非交给 curl 自动跟随）。
// 开启后每一跳的 Set-Cookie 都会写入 cookie jar，
// 适合登录流等需要中间跳转 cookie 的场景。
// 性能略差（每跳一次 Go ↔ cgo 调用），默认关闭。
func WithManualRedirects(on bool) Option {
	return func(s *Session) { s.manualRedirects = on }
}

// SetProxy 动态切换代理。
func (s *Session) SetProxy(proxy string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.proxy = proxy
}

// SetImpersonate 动态切换浏览器 profile。
func (s *Session) SetImpersonate(profile string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.impersonate = profile
}

// SetDefaultHeaders 替换会话默认 headers。
func (s *Session) SetDefaultHeaders(h OrderedHeaders) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaultHeaders = h.Clone()
}

// AddBeforeRequest / AddAfterResponse 运行时增加 hook。
func (s *Session) AddBeforeRequest(h BeforeRequestHook) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beforeHooks = append(s.beforeHooks, h)
}
func (s *Session) AddAfterResponse(h AfterResponseHook) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.afterHooks = append(s.afterHooks, h)
}

// Close 取消 session 的内部 context，并清理资源（含连接池）。
// 应在没有在途请求时调用。
func (s *Session) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	// 顺序要紧：先关连接池（cleanup 掉所有用到 share 的句柄），再释放 share。
	if s.pool != nil {
		s.pool.Close()
	}
	if s.share != nil {
		s.share.Close()
		s.share = nil
	}
}

// SetCookiesFromResponse 将响应中的 Set-Cookie 写入 jar。
// 内部使用，留作公开 API 方便手动操作。
func (s *Session) SetCookiesFromResponse(rawURL string, resp *Response) {
	if resp == nil {
		return
	}
	cookies := resp.Cookies()
	if len(cookies) == 0 {
		return
	}
	u, err := parseURL(rawURL)
	if err != nil {
		return
	}
	s.jar.SetCookies(u, cookies)
}

// 内部快照（避免锁覆盖整个请求生命周期）
type sessionSnapshot struct {
	impersonate     string
	timeout         time.Duration
	proxy           string
	followRedirects bool
	maxRedirects    int
	defaultHeaders  OrderedHeaders
	autoUA          bool
	insecureTLS     bool
	maxBodyBytes    int64
	blockPrivateIPs bool
	autoDecode      bool
	beforeHooks     []BeforeRequestHook
	afterHooks      []AfterResponseHook
	manualRedirects bool
	identity        *IdentityRotator
}

func (s *Session) snapshot() sessionSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sessionSnapshot{
		impersonate:     s.impersonate,
		timeout:         s.timeout,
		proxy:           s.proxy,
		followRedirects: s.followRedirects,
		maxRedirects:    s.maxRedirects,
		defaultHeaders:  s.defaultHeaders.Clone(),
		autoUA:          s.autoUA,
		insecureTLS:     s.insecureTLS,
		maxBodyBytes:    s.maxBodyBytes,
		blockPrivateIPs: s.blockPrivateIPs,
		autoDecode:      s.autoDecode,
		beforeHooks:     append([]BeforeRequestHook(nil), s.beforeHooks...),
		afterHooks:      append([]AfterResponseHook(nil), s.afterHooks...),
		manualRedirects: s.manualRedirects,
		identity:        s.identity, // 轮换器并发安全，可直接共享指针
	}
}
