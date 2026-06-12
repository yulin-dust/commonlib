package lclient

import (
	"context"
	"time"

	"golang.org/x/time/rate"
)

// rateLimiter 简单包装 golang.org/x/time/rate。
type rateLimiter struct {
	limiter *rate.Limiter
}

func newRateLimiter(qps float64, burst int) *rateLimiter {
	if qps <= 0 {
		return nil
	}
	if burst <= 0 {
		burst = 1
	}
	return &rateLimiter{limiter: rate.NewLimiter(rate.Limit(qps), burst)}
}

// Wait 阻塞直到允许下一次请求；ctx 取消会返回错误。
func (r *rateLimiter) Wait(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return r.limiter.Wait(ctx)
}

// WaitFor 简化版：限定一个最大等待时间。
func (r *rateLimiter) WaitFor(timeout time.Duration) error {
	if r == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return r.limiter.Wait(ctx)
}
