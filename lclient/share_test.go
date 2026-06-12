package lclient

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// 共享缓存的句柄基本可用：归还/复用 + 共享 DNS 缓存不报错。
func TestSharedCacheBasic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	s := NewSession(WithSharedCache(), WithConnectionPool(2))
	defer s.Close()
	for i := 0; i < 5; i++ {
		resp, err := s.Get(srv.URL)
		if err != nil {
			t.Fatalf("req %d: %v", i, err)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("req %d: status %d", i, resp.StatusCode)
		}
	}
}

// 并发安全（HTTP）：共享 DNS 缓存被多 goroutine 并发使用，靠 share 的锁回调保护。
// 若锁不正确，curl 会在并发访问共享缓存时崩溃/串数据。配合 -race。
func TestSharedCacheConcurrentHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	s := NewSession(WithSharedCache(), WithConnectionPool(4))
	defer s.Close()
	runConcurrent(t, s, srv.URL, 30)
}

// 并发安全（HTTPS）：额外触发 TLS 会话缓存（SSL_SESSION）的共享路径。
// 自签证书 → 需 InsecureSkipVerify。
func TestSharedCacheConcurrentHTTPS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	s := NewSession(WithSharedCache(), WithConnectionPool(4), WithInsecureSkipVerify(true))
	defer s.Close()
	runConcurrent(t, s, srv.URL, 30)
}

func runConcurrent(t *testing.T, s *Session, url string, n int) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			resp, err := s.Get(url)
			if err != nil {
				errs <- fmt.Errorf("g%d: %w", k, err)
				return
			}
			if resp.StatusCode != 200 {
				errs <- fmt.Errorf("g%d: status %d", k, resp.StatusCode)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
