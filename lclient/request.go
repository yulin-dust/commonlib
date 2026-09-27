package lclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/url"
	"os"
	"strings"
	"time"

	curl "github.com/yulin-dust/commonlib/third_party/curlimpersonate"
	"golang.org/x/net/http/httpproxy"
)

// envProxyFunc 复刻 libcurl 对环境变量代理（http_proxy/https_proxy/all_proxy，
// 并遵守 no_proxy）的判定逻辑，用于判断某个 URL 实际是否会走代理。
// 在包加载时读取一次环境变量（长期运行的爬虫进程足够）。
var envProxyFunc = httpproxy.FromEnvironment().ProxyFunc()

// usesProxy 报告本次请求是否会经由代理（显式代理或匹配到的环境变量代理）。
// SSRF 的"连接 IP 私网拦截"只对直连有效：走代理时 curl 连的是代理、由代理解析
// 目标，连接 IP 是代理地址，按它判定既错误又无意义，故此时跳过该检查。
func usesProxy(explicitProxy, rawURL string) bool {
	if explicitProxy != "" {
		return true
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	pu, err := envProxyFunc(u)
	return err == nil && pu != nil
}

// normalizeProxyForCurl 把 `https://` 代理 URL 改写为 `http://`。
//
// 背景：本地 fork 已设 CURLOPT_PROXY_SSL_VERIFYPEER/VERIFYHOST=0，https:// 代理
// 其实可直接使用。此改写仅作为对老配置的兜底保险——多数隧道代理（QG / XMDL /
// 公司隧道代理 / squid）走 HTTP CONNECT，不要求到代理本身用 TLS。改成 http:// 后：
//   - 客户端 → 代理：明文 CONNECT 握手
//   - 代理 → 目标 HTTPS：CONNECT 隧道内仍是 TLS（端到端加密不变）
//
// 即对**目标站点的 HTTPS 通信**没有任何影响，只是不再要求"到代理本身"是 TLS。
//
// 返回 (规范化后的 URL, 是否被改写)。空字符串不变。
func normalizeProxyForCurl(proxy string) (string, bool) {
	if proxy == "" {
		return "", false
	}
	// 大小写不敏感识别 scheme
	if len(proxy) >= 8 && strings.EqualFold(proxy[:8], "https://") {
		return "http://" + proxy[8:], true
	}
	return proxy, false
}

// RequestOption 是一次请求级选项的统一接口。
// 通过类型断言区分不同 option。
type RequestOption interface {
	applyToRequest(r *PreparedRequest, s sessionSnapshot)
}

// ============================================================
// 各类请求级 option 实现（让 Get/Post 等签名简洁）
// ============================================================

// Params / OrderedParams 实现 RequestOption
func (p Params) applyToRequest(r *PreparedRequest, _ sessionSnapshot) {
	if len(p) == 0 {
		return
	}
	r.URL = appendQuery(r.URL, p.Encode())
}
func (p OrderedParams) applyToRequest(r *PreparedRequest, _ sessionSnapshot) {
	if len(p) == 0 {
		return
	}
	r.URL = appendQuery(r.URL, p.Encode())
}

// Headers / OrderedHeaders 实现 RequestOption
func (h Headers) applyToRequest(r *PreparedRequest, _ sessionSnapshot) {
	r.Headers.MergeMap(h)
}
func (h OrderedHeaders) applyToRequest(r *PreparedRequest, _ sessionSnapshot) {
	r.Headers.Merge(h)
}

// Body 实现 RequestOption
func (b *Body) applyToRequest(r *PreparedRequest, _ sessionSnapshot) {
	if b == nil {
		return
	}
	if b.err != nil {
		r.bodyErr = b.err
		return
	}
	r.Body = b.Bytes()
	if !r.Headers.Has("Content-Type") && b.ContentType() != "" {
		r.Headers.Set("Content-Type", b.ContentType())
	}
}

// 单次覆盖：超时、代理、profile、重定向
type oTimeout struct{ d time.Duration }
type oProxy struct{ s string }
type oProfile struct{ s string }
type oFollow struct{ b bool }
type oInsecure struct{ b bool }
type oMaxBody struct{ n int64 }

func (o oTimeout) applyToRequest(r *PreparedRequest, _ sessionSnapshot) {
	r.Timeout = o.d
}
func (o oProxy) applyToRequest(r *PreparedRequest, _ sessionSnapshot)    { r.Proxy = o.s }
func (o oProfile) applyToRequest(r *PreparedRequest, _ sessionSnapshot)  { r.Profile = o.s }
func (o oFollow) applyToRequest(r *PreparedRequest, _ sessionSnapshot)   { r.FollowRedirects = o.b }
func (o oInsecure) applyToRequest(r *PreparedRequest, _ sessionSnapshot) { r.InsecureTLS = o.b }
func (o oMaxBody) applyToRequest(r *PreparedRequest, _ sessionSnapshot) {
	if o.n < 0 {
		o.n = 0
	}
	r.MaxBodyBytes = o.n
}

// MaxResponseBytes 单次请求的响应体大小上限覆盖（0 = 不限制）。
// 超限返回的错误可用 errors.Is(err, ErrBodyTooLarge) 判定。
func MaxResponseBytes(n int64) RequestOption { return oMaxBody{n} }

// Timeout 单次请求超时覆盖。
func Timeout(d time.Duration) RequestOption { return oTimeout{d} }

// 单次覆盖：context
type oCtx struct{ ctx context.Context }

func (o oCtx) applyToRequest(r *PreparedRequest, _ sessionSnapshot) {
	r.Ctx = o.ctx
}

// Ctx 单次请求级 context（用于超时 / 取消）。
// ctx 取消会中断重试等待、限流等待，并通过 curl 进度回调中止"在途"的 HTTP 传输：
// 连接挂起、无数据流动时进度回调约每秒触发一次，故中止延迟通常在 1 秒内，
// 不必干等 curl 内部超时（可能长达数十秒）。仍建议配合 Timeout 作为最终兜底。
func Ctx(ctx context.Context) RequestOption { return oCtx{ctx} }

// Proxy 单次请求代理覆盖。
func Proxy(p string) RequestOption { return oProxy{p} }

// Impersonate 单次请求 profile 覆盖。
func Impersonate(profile string) RequestOption { return oProfile{profile} }

// FollowRedirects 单次请求重定向开关覆盖。
func FollowRedirects(b bool) RequestOption { return oFollow{b} }

// InsecureSkipVerify 单次请求跳过目标 TLS 证书校验（默认校验，谨慎使用）。
func InsecureSkipVerify(b bool) RequestOption { return oInsecure{b} }

// ============================================================
// multipart/form-data
// ============================================================

// MultipartField 一个 multipart 字段。
type MultipartField struct {
	Name     string
	Value    string // 文本字段
	FilePath string // 文件字段；与 Value 互斥
	Filename string // 自定义文件名（可选）
	MimeType string // 自定义 mime（可选）
}

// Multipart 构造 multipart/form-data body。
// 注意 multipart 的 boundary 浏览器有特定模式，
// 若风控严，建议自己拼装 body 并通过 RawBytes 传入。
func Multipart(fields []MultipartField) (*Body, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, f := range fields {
		if f.FilePath != "" {
			file, err := os.Open(f.FilePath)
			if err != nil {
				return nil, fmt.Errorf("open file %s: %w", f.FilePath, err)
			}
			filename := f.Filename
			if filename == "" {
				filename = filepathBase(f.FilePath)
			}
			fw, err := w.CreateFormFile(f.Name, filename)
			if err != nil {
				file.Close()
				return nil, err
			}
			if _, err := io.Copy(fw, file); err != nil {
				file.Close()
				return nil, err
			}
			file.Close()
		} else {
			if err := w.WriteField(f.Name, f.Value); err != nil {
				return nil, err
			}
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return &Body{raw: buf.Bytes(), contentType: w.FormDataContentType()}, nil
}

func filepathBase(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}

// ============================================================
// 方法快捷入口
// ============================================================

func (s *Session) Get(url string, opts ...RequestOption) (*Response, error) {
	return s.Do("GET", url, opts...)
}
func (s *Session) Post(url string, opts ...RequestOption) (*Response, error) {
	return s.Do("POST", url, opts...)
}
func (s *Session) Put(url string, opts ...RequestOption) (*Response, error) {
	return s.Do("PUT", url, opts...)
}
func (s *Session) Delete(url string, opts ...RequestOption) (*Response, error) {
	return s.Do("DELETE", url, opts...)
}
func (s *Session) Patch(url string, opts ...RequestOption) (*Response, error) {
	return s.Do("PATCH", url, opts...)
}
func (s *Session) Head(url string, opts ...RequestOption) (*Response, error) {
	return s.Do("HEAD", url, opts...)
}
func (s *Session) Options(url string, opts ...RequestOption) (*Response, error) {
	return s.Do("OPTIONS", url, opts...)
}

// Must 版本：失败 panic，脚本场景方便。
func (s *Session) MustGet(url string, opts ...RequestOption) *Response {
	return must(s.Get(url, opts...))
}
func (s *Session) MustPost(url string, opts ...RequestOption) *Response {
	return must(s.Post(url, opts...))
}
func (s *Session) MustPut(url string, opts ...RequestOption) *Response {
	return must(s.Put(url, opts...))
}
func (s *Session) MustDelete(url string, opts ...RequestOption) *Response {
	return must(s.Delete(url, opts...))
}
func (s *Session) MustPatch(url string, opts ...RequestOption) *Response {
	return must(s.Patch(url, opts...))
}

func must(r *Response, err error) *Response {
	if err != nil {
		panic(err)
	}
	return r
}

// ============================================================
// 核心执行
// ============================================================

// Do 是所有方法的最终入口。
func (s *Session) Do(method, rawURL string, opts ...RequestOption) (*Response, error) {
	snap := s.snapshot()

	// 1. 准备请求
	prep := &PreparedRequest{
		Method:          strings.ToUpper(method),
		URL:             rawURL,
		Headers:         snap.defaultHeaders, // snapshot() 已返回独立副本，无需再 Clone
		Profile:         snap.impersonate,
		Proxy:           snap.proxy,
		Timeout:         snap.timeout,
		FollowRedirects: snap.followRedirects,
		MaxRedirects:    snap.maxRedirects,
		InsecureTLS:     snap.insecureTLS,
		MaxBodyBytes:    snap.maxBodyBytes,
		BlockPrivateIPs: snap.blockPrivateIPs,
	}

	if prep.Ctx == nil {
		prep.Ctx = s.ctx // 默认用 session 的 ctx
	}

	// 1.5 随机浏览器身份：在请求级 options 之前套用，使整套 profile + headers 自洽，
	//     同时让调用方在单次请求里显式设置的 header / Impersonate 仍能覆盖它。
	if snap.identity != nil {
		snap.identity.apply(prep)
	}

	// 2. 应用请求级 options
	for _, opt := range opts {
		opt.applyToRequest(prep, snap)
	}
	// body 构造期错误（如 JSON 序列化失败）在此显式返回，不再静默发出畸形请求
	if prep.bodyErr != nil {
		return nil, &RequestError{Op: prep.Method + " " + prep.URL, Err: prep.bodyErr}
	}

	// 2.5 超时兜底：放在请求级 options 之后，避免 Timeout(...) 把它覆盖回 0
	//     （0 传到 libcurl 是"永不超时"，不是"立刻超时"）。
	if prep.Timeout <= 0 {
		prep.Timeout = 30 * time.Second
	}

	// 3. 自动 UA
	if snap.autoUA && !prep.Headers.Has("User-Agent") {
		prep.Headers.Set("User-Agent", UserAgentFor(prep.Profile))
	}

	// 4. 从 cookie jar 注入 Cookie（仅当用户没有显式覆盖时）
	if !prep.Headers.Has("Cookie") {
		u, err := parseURL(prep.URL)
		if err != nil {
			return nil, &RequestError{Op: prep.Method + " " + prep.URL, Err: err}
		}
		if c := s.jar.CookiesAsHeader(u); c != "" {
			prep.Headers.Set("Cookie", c)
		}
	}

	// 5. before hooks
	for _, h := range snap.beforeHooks {
		if err := h(prep); err != nil {
			return nil, &RequestError{Op: prep.Method + " " + prep.URL, Err: err}
		}
	}

	// 5.5 校验最终 header：拒绝键/值含 CR/LF 等控制字符的请求，防止 header 注入
	//     （恶意的 Referer、被污染的输入等可能借此塞入额外请求头乃至走私请求）。
	//     放在最后——覆盖 options / autoUA / cookie 注入 / before-hooks produced 的全部 header。
	if err := validateHeaders(prep.Headers); err != nil {
		return nil, &RequestError{Op: prep.Method + " " + prep.URL, Err: err}
	}

	// 6. 限流
	if s.limiter != nil {
		if err := s.limiter.Wait(prep.Ctx); err != nil {
			return nil, &RequestError{
				Op:  prep.Method + " " + prep.URL,
				Err: fmt.Errorf("%w: %v", ErrCanceled, err),
			}
		}
	}

	// 7. 执行（含重试）
	resp, err := s.executeWithRetry(prep)
	if err != nil {
		return nil, err
	}

	// 7.5 可选：把非 UTF-8 响应体转成 UTF-8（GB2312/GBK/Big5 等），避免乱码。
	if snap.autoDecode && resp != nil {
		resp.Body = decodeToUTF8(resp.Body, resp.HeaderGet("Content-Type"))
	}

	// 8. 把响应 Set-Cookie 写回 jar
	s.SetCookiesFromResponse(prep.URL, resp)

	// 9. after hooks
	for _, h := range snap.afterHooks {
		if err := h(prep, resp); err != nil {
			return resp, &RequestError{Op: prep.Method + " " + prep.URL, Err: err}
		}
	}

	return resp, nil
}

func (s *Session) shouldManualRedirect(prep *PreparedRequest) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.manualRedirects && prep.FollowRedirects
}

// executeWithRetry 执行请求并按策略重试。
func (s *Session) executeWithRetry(prep *PreparedRequest) (*Response, error) {
	policy := s.retry
	maxAttempts := 1
	if policy != nil && policy.MaxAttempts > 1 {
		maxAttempts = policy.MaxAttempts
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// ctx 已取消 → 立即返回
		if err := prep.Ctx.Err(); err != nil {
			return nil, &RequestError{
				Op:  prep.Method + " " + prep.URL,
				Err: fmt.Errorf("%w: %v", ErrCanceled, err),
			}
		}
		var resp *Response
		var err error
		if /* manual redirects */ s.shouldManualRedirect(prep) {
			resp, err = s.executeWithManualRedirects(prep)
		} else {
			resp, err = s.executeOnce(prep)
		}
		if err == nil {
			// 检查状态码是否触发重试
			if policy != nil && policy.shouldRetryStatus(resp.StatusCode) && attempt < maxAttempts {
				lastErr = fmt.Errorf("http status %d", resp.StatusCode)
				sleepCtx(prep.Ctx, policy.nextDelay(attempt))
				continue
			}
			return resp, nil
		}

		lastErr = err
		// 确定性失败重试也不会变，直接原样返回——顺带保住调用方对哨兵错误的
		// errors.Is 判定（走到下面的 ErrTooManyRetries 包装时链路里仍有它们，
		// 但语义上这类错误本就不该被重试 3 次）。
		if isFatalError(err) {
			return nil, err
		}
		if attempt < maxAttempts && policy != nil {
			sleepCtx(prep.Ctx, policy.nextDelay(attempt))
			continue
		}
	}

	// 没配重试策略，直接返回原始错误
	if policy == nil || maxAttempts == 1 {
		return nil, lastErr
	}

	// 用 %w 同时包住 ErrTooManyRetries 与底层错误：若用 %v，errors.Is(err,
	// ErrTimeout) / ErrNetwork 等在开启重试后会全部失效。
	return nil, &RequestError{
		Op:  prep.Method + " " + prep.URL,
		Err: fmt.Errorf("%w: %w", ErrTooManyRetries, lastErr),
	}
}

// isFatalError 报告该错误是否确定性失败——重试同样的请求只会得到同样的结果，
// 白白放大目标站压力。取消 / 响应体超限 / SSRF 拦截 / 非法请求头都属此类。
func isFatalError(err error) bool {
	return errors.Is(err, ErrCanceled) ||
		errors.Is(err, ErrBodyTooLarge) ||
		errors.Is(err, ErrBlockedAddress) ||
		errors.Is(err, ErrInvalidHeader)
}

// executeOnce 执行单次请求。
func (s *Session) executeOnce(prep *PreparedRequest) (*Response, error) {
	start := time.Now()
	// 用 ctx deadline 调整 timeout
	timeout := prep.Timeout
	if dl, ok := prep.Ctx.Deadline(); ok {
		remain := time.Until(dl)
		if remain <= 0 {
			return nil, &RequestError{
				Op:  prep.Method + " " + prep.URL,
				Err: fmt.Errorf("%w: context deadline exceeded", ErrCanceled),
			}
		}
		if remain < timeout {
			timeout = remain
		}
	}
	// debug 打印请求
	s.debug.logRequest(prep.Method, prep.URL, prep.Headers, prep.Body)

	// 有序传递 header（顺序是反爬指纹的一部分）
	headerKeys, headerVals := prep.Headers.Split()

	// 规范化代理 URL：https:// → http://（底层 fork 已可跳过代理 TLS 校验，
	// 此改写仅为对老配置更稳妥，对目标站点 HTTPS 无影响）
	proxyToUse, _ := normalizeProxyForCurl(prep.Proxy)

	// SSRF 私网拦截只在直连时生效：走代理时连接 IP 是代理地址，按它判定会误杀
	// （且无法保护到真正的目标）。因此请求经代理时关闭该检查。
	blockPrivateIPs := prep.BlockPrivateIPs && !usesProxy(proxyToUse, prep.URL)

	curlReq := curl.Request{
		URL:             prep.URL,
		Proxy:           proxyToUse,
		Impersonate:     prep.Profile,
		Method:          prep.Method,
		HeaderKeys:      headerKeys,
		HeaderVals:      headerVals,
		Body:            prep.Body,
		FollowRedirects: prep.FollowRedirects,
		MaxRedirects:    prep.MaxRedirects,
		Timeout:         timeout,
		VerifyTLS:       !prep.InsecureTLS,
		MaxBodyBytes:    prep.MaxBodyBytes,
		BlockPrivateIPs: blockPrivateIPs,
		Share:           s.share,  // 跨句柄共享 DNS / TLS 会话缓存（nil 即不共享）
		Ctx:             prep.Ctx, // 让 ctx 取消 / 超时能中止在途传输
	}

	// 启用连接池时，借一个可复用句柄发请求、用完归还（复用其连接 / TLS 会话 /
	// DNS 缓存）；否则走一次性句柄。
	var rawResp *curl.Response
	var err error
	if s.pool != nil {
		var h *curl.Handle
		h, err = s.pool.get()
		if err != nil {
			return nil, &RequestError{Op: prep.Method + " " + prep.URL, Err: err}
		}
		rawResp, err = h.Do(curlReq)
		s.pool.put(h) // 即便本次出错也归还：curl 会自行丢弃失活连接并重连
	} else {
		rawResp, err = curl.Do(curlReq)
	}
	if err != nil {
		return nil, &RequestError{
			Op:  prep.Method + " " + prep.URL,
			Err: classifyError(err),
		}
	}

	resp := buildResponse(rawResp, prep.URL, time.Since(start))

	// debug 打印响应
	s.debug.logResponse(resp.StatusCode, resp.Header, resp.Body, resp.Elapsed.String())

	return resp, nil
}

// sleepCtx 在 ctx 取消时立刻返回。
func sleepCtx(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// validateHeaders 校验请求头是否安全可发送：键不得为空、键值都不得含 CR/LF 或
// 其它 C0 控制字符（NUL/制表符以外的 <0x20，外加 DEL）。这能挡住 CRLF header 注入
// 与请求走私——这类攻击常来自被污染的 header 值（如从重定向 URL 取的 Referer）。
func validateHeaders(h OrderedHeaders) error {
	for _, kv := range h {
		if kv[0] == "" {
			return fmt.Errorf("%w: empty header name", ErrInvalidHeader)
		}
		if i := strings.IndexFunc(kv[0], isCtl); i >= 0 {
			return fmt.Errorf("%w: control char in header name %q", ErrInvalidHeader, kv[0])
		}
		if i := strings.IndexFunc(kv[1], isCtl); i >= 0 {
			return fmt.Errorf("%w: control char in value of header %q", ErrInvalidHeader, kv[0])
		}
	}
	return nil
}

// isCtl 报告 r 是否为不允许出现在 header 里的控制字符。
// 允许水平制表符（\t，HTTP 头折叠的合法字符）；其余 <0x20 与 0x7f(DEL) 一律拒绝。
func isCtl(r rune) bool {
	return (r < 0x20 && r != '\t') || r == 0x7f
}

// appendQuery 把 query string 追加到 URL。
func appendQuery(rawURL, q string) string {
	if q == "" {
		return rawURL
	}
	if strings.Contains(rawURL, "?") {
		return rawURL + "&" + q
	}
	return rawURL + "?" + q
}

// parseURL 包一层错误提示。
func parseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid url %q: %w", raw, err)
	}
	return u, nil
}

// executeWithManualRedirects 由 lclient 自己跟 3xx，每跳都写 cookie jar。
func (s *Session) executeWithManualRedirects(prep *PreparedRequest) (*Response, error) {
	// 限制最大跳数（session.maxRedirects 已经在 session 中）
	s.mu.RLock()
	maxRedirects := s.maxRedirects
	s.mu.RUnlock()
	if maxRedirects <= 0 {
		maxRedirects = 10
	}

	currentURL := prep.URL
	currentMethod := prep.Method
	currentBody := prep.Body
	currentHeaders := prep.Headers.Clone()

	var lastResp *Response

	for hop := 0; hop <= maxRedirects; hop++ {
		// 每跳重新从 jar 注入 Cookie
		hopHeaders := currentHeaders.Clone()
		hopHeaders.Delete("Cookie")
		if u, err := parseURL(currentURL); err == nil {
			if c := s.jar.CookiesAsHeader(u); c != "" {
				hopHeaders.Set("Cookie", c)
			}
		}

		// 用 followRedirects=false 调底层（我们自己跟）
		hopPrep := &PreparedRequest{
			Method:          currentMethod,
			URL:             currentURL,
			Headers:         hopHeaders,
			Body:            currentBody,
			Proxy:           prep.Proxy,
			Profile:         prep.Profile,
			Timeout:         prep.Timeout,
			FollowRedirects: false,
			InsecureTLS:     prep.InsecureTLS, // 跟随每一跳都应沿用 TLS 校验设置
			MaxBodyBytes:    prep.MaxBodyBytes,
			BlockPrivateIPs: prep.BlockPrivateIPs, // 每一跳都要做 SSRF 校验
			Ctx:             prep.Ctx,
		}

		resp, err := s.executeOnce(hopPrep)
		if err != nil {
			return lastResp, err
		}
		// 与 curl 自动跟随模式保持一致：URL 始终是最初发起的地址，
		// FinalURL 才是跟完之后的落点。
		resp.URL = prep.URL
		lastResp = resp

		// 写入 cookie jar
		s.SetCookiesFromResponse(currentURL, resp)

		// 不是 3xx 或没有 Location → 结束
		if resp.StatusCode < 300 || resp.StatusCode >= 400 {
			return resp, nil
		}
		location := resp.HeaderGet("Location")
		if location == "" {
			return resp, nil
		}

		// 解析新 URL（支持相对路径）
		nextURL, err := resolveURL(currentURL, location)
		if err != nil {
			return resp, fmt.Errorf("invalid Location %q: %w", location, err)
		}

		// 根据状态码决定下一跳方法和 body
		// 303 → 强制 GET；301/302 → 一般也降为 GET（浏览器行为）；307/308 → 保持方法和 body
		switch resp.StatusCode {
		case 303, 301, 302:
			currentMethod = "GET"
			currentBody = nil
			// 清掉只属于 POST 的 header
			currentHeaders.Delete("Content-Type")
			currentHeaders.Delete("Content-Length")
		case 307, 308:
			// 保持 method 和 body
		default:
			// 其他 3xx 不处理
			return resp, nil
		}

		currentURL = nextURL
	}

	return lastResp, fmt.Errorf("too many redirects (>%d)", maxRedirects)
}

// resolveURL 把 location 相对路径解析成绝对 URL。
func resolveURL(base, location string) (string, error) {
	baseURL, err := parseURL(base)
	if err != nil {
		return "", err
	}
	locURL, err := parseURL(location)
	if err != nil {
		return "", err
	}
	return baseURL.ResolveReference(locURL).String(), nil
}
