package lclient

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

// 并发场景：一边大量请求（读+写 cookie jar），一边并发 Clear()/读 jar，
// 验证不 panic、无数据竞争（-race）。
func TestConcurrentRequestsAndCookieClear(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "abc", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "t", Value: "1", Path: "/"})
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	s := NewSession(WithConnectionPool(4), WithSharedCache())
	defer s.Close()
	u, _ := url.Parse(srv.URL)

	var wg sync.WaitGroup

	// 20 个请求者：每次 Get 都会从 jar 读 Cookie、再把 Set-Cookie 写回 jar。
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 15; j++ {
				if resp, err := s.Get(srv.URL); err != nil || resp.StatusCode != 200 {
					t.Errorf("请求失败: %v", err)
					return
				}
			}
		}()
	}
	// 5 个清理者：并发 Clear 整个 jar。
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 30; j++ {
				s.Jar().Clear()
			}
		}()
	}
	// 5 个读者：并发读 jar / 导出。
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 30; j++ {
				_ = s.Jar().CookiesAsHeader(u)
				_ = s.Jar().ExportAll()
				s.Jar().ClearDomain("127.0.0.1")
			}
		}()
	}

	wg.Wait()
}

// 并发改 session 配置（SetProxy/SetImpersonate/SetDefaultHeaders）+ 请求，验证 snapshot 机制无竞争。
func TestConcurrentRequestsAndConfigMutation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	s := NewSession()
	defer s.Close()

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 15; j++ {
				_, _ = s.Get(srv.URL)
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				s.SetImpersonate(Chrome120)
				s.SetDefaultHeaders(OrderedHeaders{{"X-K", "v"}})
			}
		}()
	}
	wg.Wait()
}
