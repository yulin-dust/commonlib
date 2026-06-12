package lclient

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// 连接复用：顺序打同一站点多次，应都成功；打印首次 vs 后续耗时以肉眼核对握手节省。
func TestLiveConnectionPoolSequential(t *testing.T) {
	if os.Getenv("LCLIENT_LIVE") != "1" {
		t.Skip("set LCLIENT_LIVE=1")
	}
	s := NewSession(WithConnectionPool(2))
	defer s.Close()
	for i := 0; i < 4; i++ {
		start := time.Now()
		resp, err := s.Get("https://postman-echo.com/get")
		if err != nil {
			t.Fatalf("req %d: %v", i, err)
		}
		if resp.StatusCode != 200 {
			t.Logf("req %d status %d", i, resp.StatusCode)
		}
		t.Logf("req %d: %v (status %d)", i, time.Since(start).Round(time.Millisecond), resp.StatusCode)
	}
}

// 并发安全：池只有 4 个句柄，20 个 goroutine 抢用。若句柄被并发共享，curl 会崩溃/串数据。
func TestLiveConnectionPoolConcurrent(t *testing.T) {
	if os.Getenv("LCLIENT_LIVE") != "1" {
		t.Skip("set LCLIENT_LIVE=1")
	}
	s := NewSession(WithConnectionPool(4))
	defer s.Close()
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			resp, err := s.Get("https://postman-echo.com/get")
			if err != nil {
				errs <- fmt.Errorf("g%d: %w", n, err)
				return
			}
			if resp.StatusCode != 200 {
				errs <- fmt.Errorf("g%d: status %d", n, resp.StatusCode)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
