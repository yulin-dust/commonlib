package lclient

import (
	"errors"
	"fmt"
)

// 错误分类：让调用方可以用 errors.Is 精准判断。
var (
	// ErrNetwork 网络层错误（DNS、连接拒绝、连接重置等）
	ErrNetwork = errors.New("network error")
	// ErrTimeout 请求超时
	ErrTimeout = errors.New("timeout")
	// ErrTooManyRetries 重试耗尽
	ErrTooManyRetries = errors.New("too many retries")
	// ErrCanceled 请求被取消（如限流、上下文）
	ErrCanceled = errors.New("canceled")
	// ErrHTTP HTTP 状态码错误（仅在显式校验时返回）
	ErrHTTP = errors.New("http error")
	// ErrBodyTooLarge 响应体超过设定的大小上限（见 WithMaxResponseBytes）
	ErrBodyTooLarge = errors.New("response body too large")
	// ErrBlockedAddress 目标解析到被拦截的私网/环回/链路本地地址（见 WithBlockPrivateIPs）
	ErrBlockedAddress = errors.New("blocked address")
	// ErrInvalidHeader 请求头非法（如键/值含 CR/LF，可能是 header 注入）
	ErrInvalidHeader = errors.New("invalid header")
)

// RequestError 包装底层错误，附加请求上下文。
type RequestError struct {
	Op  string // 操作描述，如 "Get https://..."
	Err error
}

func (e *RequestError) Error() string {
	return fmt.Sprintf("%s: %v", e.Op, e.Err)
}

func (e *RequestError) Unwrap() error { return e.Err }

// 让 errors.Is 能匹配预定义错误。
func (e *RequestError) Is(target error) bool {
	return errors.Is(e.Err, target)
}

// classifyError 根据底层错误消息分类。
// curl 错误码字符串匹配。
func classifyError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case contains(msg, "exceeds max size limit"):
		return fmt.Errorf("%w: %v", ErrBodyTooLarge, err)
	case contains(msg, "blocked private or loopback"):
		return fmt.Errorf("%w: %v", ErrBlockedAddress, err)
	case contains(msg, "canceled by context"):
		return fmt.Errorf("%w: %v", ErrCanceled, err)
	case contains(msg, "timeout", "timed out", "operation timed out"):
		return fmt.Errorf("%w: %v", ErrTimeout, err)
	case contains(msg,
		"could not resolve host",
		"connection refused",
		"connection reset",
		"could not connect",
		"network is unreachable",
		"ssl",
		"tls",
	):
		return fmt.Errorf("%w: %v", ErrNetwork, err)
	}
	return err
}

func contains(s string, subs ...string) bool {
	for _, sub := range subs {
		if indexFold(s, sub) >= 0 {
			return true
		}
	}
	return false
}

// indexFold 大小写不敏感的 strings.Index。避免引入额外依赖。
func indexFold(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	if len(sub) > len(s) {
		return -1
	}
	lower := func(r byte) byte {
		if r >= 'A' && r <= 'Z' {
			return r + 32
		}
		return r
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		ok := true
		for j := 0; j < len(sub); j++ {
			if lower(s[i+j]) != lower(sub[j]) {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}
