package lclient

import "context"

// BeforeRequestHook 在请求发出前调用，可修改请求字段。
// 返回 error 会中止该请求。
type BeforeRequestHook func(req *PreparedRequest) error

// AfterResponseHook 在响应到达后调用，可读取/修改响应。
type AfterResponseHook func(req *PreparedRequest, resp *Response) error

// PreparedRequest 是即将发往底层的请求快照，传给中间件。
type PreparedRequest struct {
	Method          string
	URL             string
	Headers         OrderedHeaders
	Body            []byte
	Proxy           string
	Profile         string
	Timeout         int // seconds
	FollowRedirects bool
	InsecureTLS     bool            // true 时跳过目标 TLS 证书校验（默认 false=校验）
	MaxBodyBytes    int64           // 响应体大小上限（字节），0 = 不限制
	BlockPrivateIPs bool            // true 时拦截私网/环回/链路本地地址（SSRF 防护）
	Ctx             context.Context // 新增

	// bodyErr 记录 body 构造期错误（如 JSON 序列化失败），由 Do 在发请求前检查并返回。
	bodyErr error
}
