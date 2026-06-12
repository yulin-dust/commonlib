package lclient

import (
	"math"
	"math/rand"
	"time"
)

// RetryPolicy 控制重试行为。
type RetryPolicy struct {
	MaxAttempts int           // 最大尝试次数（含首次）；<=1 表示不重试
	BaseDelay   time.Duration // 初始退避
	MaxDelay    time.Duration // 最长退避
	Jitter      bool          // 是否加随机抖动
	OnStatus    []int         // 触发重试的 HTTP 状态码（如 429, 500, 502, 503, 504）
}

// DefaultRetryPolicy 生产常用默认。
func DefaultRetryPolicy() *RetryPolicy {
	return &RetryPolicy{
		MaxAttempts: 3,
		BaseDelay:   500 * time.Millisecond,
		MaxDelay:    10 * time.Second,
		Jitter:      true,
		OnStatus:    []int{429, 500, 502, 503, 504},
	}
}

// shouldRetryStatus 判断状态码是否需要重试。
func (p *RetryPolicy) shouldRetryStatus(code int) bool {
	if p == nil {
		return false
	}
	for _, c := range p.OnStatus {
		if c == code {
			return true
		}
	}
	return false
}

// nextDelay 计算第 attempt 次（1-based）失败后的等待时长。
func (p *RetryPolicy) nextDelay(attempt int) time.Duration {
	if p == nil {
		return 0
	}
	base := float64(p.BaseDelay)
	d := base * math.Pow(2, float64(attempt-1))
	if d > float64(p.MaxDelay) {
		d = float64(p.MaxDelay)
	}
	if p.Jitter {
		d *= 0.5 + rand.Float64()*0.5 // 50%~100%
	}
	return time.Duration(d)
}
