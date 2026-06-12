package lclient

import (
	"os"
	"sync"
	"testing"
)

// 所有特性组合：身份轮换 + 连接池 + 共享缓存 + SSRF 防护 + 响应体上限，并发跑。
func TestLiveKitchenSink(t *testing.T) {
	if os.Getenv("LCLIENT_LIVE") != "1" {
		t.Skip("set LCLIENT_LIVE=1")
	}
	s := NewSession(
		WithRandomIdentity(WithRandomAcceptLanguage()),
		WithConnectionPool(4),
		WithSharedCache(),
		WithBlockPrivateIPs(true),
		WithMaxResponseBytes(5<<20),
	)
	defer s.Close()
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := s.Get("https://postman-echo.com/get")
			if err != nil {
				errs <- err
				return
			}
			if resp.StatusCode != 200 {
				t.Logf("status %d", resp.StatusCode)
			}
		}()
	}
	wg.Wait()
	close(errs)
	n := 0
	for e := range errs {
		t.Error(e)
		n++
	}
	if n == 0 {
		t.Log("12 并发请求全部成功（全特性组合）")
	}
}
