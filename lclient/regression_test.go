package lclient

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 本文件锁住五个曾经的缺陷，防止回归。全部打本地 httptest，离线、确定性。

// 1. 开启重试后，哨兵错误仍能用 errors.Is 判定（曾用 %v 切断错误链）。
func TestRetryKeepsSentinelError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, 1<<16))
	}))
	defer srv.Close()

	s := NewSession(WithMaxResponseBytes(1024), WithRetry(DefaultRetryPolicy()))
	defer s.Close()

	_, err := s.Get(srv.URL)
	if err == nil {
		t.Fatal("期望超限报错，实际成功")
	}
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Errorf("errors.Is(err, ErrBodyTooLarge) = false，err = %v", err)
	}
}

// 1b. 确定性失败不应被反复重试。
func TestFatalErrorNotRetried(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Write(make([]byte, 1<<16))
	}))
	defer srv.Close()

	s := NewSession(WithMaxResponseBytes(1024), WithRetry(DefaultRetryPolicy()))
	defer s.Close()

	if _, err := s.Get(srv.URL); err == nil {
		t.Fatal("期望超限报错")
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("响应体超限被重试了：请求了 %d 次，期望 1 次", n)
	}
}

// 2. 自动跟随重定向时，header 必须取最终一跳；Set-Cookie 则要保留每一跳。
func TestRedirectHeadersUseFinalHop(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=gb2312")
		w.Header().Set("X-Hop", "first")
		http.SetCookie(w, &http.Cookie{Name: "hop1", Value: "1", Path: "/"})
		http.Redirect(w, r, "/b", 302)
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Hop", "second")
		http.SetCookie(w, &http.Cookie{Name: "hop2", Value: "2", Path: "/"})
		w.Write([]byte(`{"ok":true}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := NewSession()
	defer s.Close()

	resp, err := s.Get(srv.URL + "/a")
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.HeaderGet("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q，期望最终一跳的 application/json", got)
	}
	if got := resp.HeaderValues("X-Hop"); len(got) != 1 || got[0] != "second" {
		t.Errorf("X-Hop = %v，期望仅最终一跳 [second]", got)
	}
	if got := resp.Status; !strings.HasPrefix(got, "200") {
		t.Errorf("Status = %q，期望最终一跳的 200", got)
	}
	// Set-Cookie 例外：每一跳都要留下，否则中间跳的登录态会丢。
	if got := len(resp.Cookies()); got != 2 {
		t.Errorf("Cookies 数 = %d，期望 2（两跳各一个）", got)
	}
	// body 不该被第一跳的 gb2312 二次解码。
	if string(resp.Body) != `{"ok":true}` {
		t.Errorf("body 被错误解码：%q", resp.Body)
	}
}

// 3. POST 遇到 301/302/303 应降级为 GET 且不重发 body（曾因 CUSTOMREQUEST 保持 POST）。
func TestPostRedirectDowngradesToGet(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	record := func(m string) {
		mu.Lock()
		defer mu.Unlock()
		methods = append(methods, m)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		record(r.Method)
		http.Redirect(w, r, "/b", 302)
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		record(r.Method)
		w.Write([]byte("done"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := NewSession()
	defer s.Close()

	if _, err := s.Post(srv.URL+"/a", Raw("x=1", "text/plain")); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"POST", "GET"}
	if len(methods) != 2 || methods[0] != want[0] || methods[1] != want[1] {
		t.Errorf("服务端看到的方法 = %v，期望 %v", methods, want)
	}
}

// 3b. 307/308 必须保持方法与 body。
func TestPostRedirect307KeepsMethod(t *testing.T) {
	var mu sync.Mutex
	var methods, bodies []string
	mux := http.NewServeMux()
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		methods = append(methods, r.Method)
		mu.Unlock()
		http.Redirect(w, r, "/b", 307)
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 16)
		n, _ := r.Body.Read(buf)
		mu.Lock()
		methods = append(methods, r.Method)
		bodies = append(bodies, string(buf[:n]))
		mu.Unlock()
		w.Write([]byte("done"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := NewSession()
	defer s.Close()

	if _, err := s.Post(srv.URL+"/a", Raw("x=1", "text/plain")); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(methods) != 2 || methods[1] != "POST" {
		t.Errorf("307 后方法 = %v，期望第二跳仍是 POST", methods)
	}
	if len(bodies) != 1 || bodies[0] != "x=1" {
		t.Errorf("307 后 body = %v，期望 [x=1]", bodies)
	}
}

// 4. WithMaxRedirects 在默认（curl 自动跟随）模式下必须生效。
func TestMaxRedirectsEnforced(t *testing.T) {
	var hops int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hops, 1)
		http.Redirect(w, r, "/next", 302)
	}))
	defer srv.Close()

	s := NewSession(WithMaxRedirects(2))
	defer s.Close()

	if _, err := s.Get(srv.URL); err == nil {
		t.Fatal("期望撞上重定向上限报错")
	}
	// 首个请求 + 2 次跟随 = 3 次；上限之前是 libcurl 默认的 30。
	if n := atomic.LoadInt32(&hops); n != 3 {
		t.Errorf("服务端被请求 %d 次，期望 3 次（1 首请求 + 2 跳上限）", n)
	}
}

// 5. 亚秒级 Timeout 必须真的超时（曾被 int(d.Seconds()) 截成 0 = 永不超时）。
func TestSubSecondTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
	}))
	defer srv.Close()

	s := NewSession()
	defer s.Close()

	start := time.Now()
	_, err := s.Get(srv.URL, Timeout(300*time.Millisecond))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("期望超时报错，实际成功")
	}
	if !errors.Is(err, ErrTimeout) {
		t.Errorf("errors.Is(err, ErrTimeout) = false，err = %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("耗时 %v，说明 300ms 超时没生效", elapsed)
	}
}

// ---- 以下锁住几个次要缺陷 ----

// 6. Response.FinalURL 必须是跟随完重定向后的落点（曾完全没暴露）。
func TestFinalURLAfterRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/sub/b", 302)
	})
	mux.HandleFunc("/sub/b", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("done"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	for _, manual := range []bool{false, true} {
		var opts []Option
		if manual {
			opts = append(opts, WithManualRedirects(true))
		}
		s := NewSession(opts...)
		resp, err := s.Get(srv.URL + "/a")
		if err != nil {
			s.Close()
			t.Fatal(err)
		}
		if want := srv.URL + "/sub/b"; resp.FinalURL != want {
			t.Errorf("manual=%v: FinalURL = %q，期望 %q", manual, resp.FinalURL, want)
		}
		if want := srv.URL + "/a"; resp.URL != want {
			t.Errorf("manual=%v: URL = %q，期望最初发起的 %q", manual, resp.URL, want)
		}
		s.Close()
	}
}

// 7. 没带 Domain 属性的 cookie 是 host-only，不该外溢到子域。
func TestCookieHostOnlyNotSentToSubdomain(t *testing.T) {
	jar := NewCookieJar()
	parent, _ := url.Parse("https://example.com/")

	jar.SetCookies(parent, []*http.Cookie{
		{Name: "hostonly", Value: "1", Path: "/"},                      // 无 Domain
		{Name: "shared", Value: "2", Path: "/", Domain: "example.com"}, // 显式 Domain
	})

	sub, _ := url.Parse("https://a.example.com/")
	got := jar.CookiesAsHeader(sub)
	if strings.Contains(got, "hostonly") {
		t.Errorf("host-only cookie 外溢到子域了: %q", got)
	}
	if !strings.Contains(got, "shared=2") {
		t.Errorf("带 Domain 的 cookie 没发给子域: %q", got)
	}
	// 原 host 上两个都要发。
	if h := jar.CookiesAsHeader(parent); !strings.Contains(h, "hostonly=1") || !strings.Contains(h, "shared=2") {
		t.Errorf("原 host 上 cookie 不全: %q", h)
	}
}

// 8. path 匹配必须落在 "/" 边界上，/ab 的 cookie 不该发给 /abc。
func TestCookiePathBoundary(t *testing.T) {
	jar := NewCookieJar()
	u, _ := url.Parse("https://example.com/ab")
	jar.SetCookies(u, []*http.Cookie{{Name: "p", Value: "1", Path: "/ab"}})

	cases := map[string]bool{
		"https://example.com/ab":     true,
		"https://example.com/ab/":    true,
		"https://example.com/ab/c":   true,
		"https://example.com/abc":    false,
		"https://example.com/abcdef": false,
		"https://example.com/":       false,
	}
	for raw, want := range cases {
		ru, _ := url.Parse(raw)
		got := jar.CookiesAsHeader(ru) != ""
		if got != want {
			t.Errorf("%s: 携带 cookie = %v，期望 %v", raw, got, want)
		}
	}
}

// 9. canonicalHost 不能把 IPv6 字面量切坏。
func TestCanonicalHostIPv6(t *testing.T) {
	cases := map[string]string{
		"[::1]:8080":      "::1",
		"[::1]":           "::1",
		"Example.COM:443": "example.com",
		"example.com":     "example.com",
		"[fe80::1%25en0]": "fe80::1%25en0",
	}
	for in, want := range cases {
		if got := canonicalHost(in); got != want {
			t.Errorf("canonicalHost(%q) = %q，期望 %q", in, got, want)
		}
	}
	// 端到端：IPv6 host 的 cookie 存取要能对上。
	jar := NewCookieJar()
	u, _ := url.Parse("http://[::1]:8080/")
	jar.SetCookies(u, []*http.Cookie{{Name: "v6", Value: "1", Path: "/"}})
	if got := jar.CookiesAsHeader(u); got != "v6=1" {
		t.Errorf("IPv6 host cookie 取不回来: %q", got)
	}
}

// 10. cookie 持久化要能往返，且 host-only 标记不丢。
func TestCookieRoundTripPreservesHostOnly(t *testing.T) {
	jar := NewCookieJar()
	u, _ := url.Parse("https://example.com/")
	jar.SetCookies(u, []*http.Cookie{
		{Name: "hostonly", Value: "1", Path: "/"},
		{Name: "shared", Value: "2", Path: "/", Domain: "example.com"},
	})

	path := t.TempDir() + "/cookies.json"
	if err := jar.SaveToFile(path); err != nil {
		t.Fatal(err)
	}
	loaded := NewCookieJar()
	if err := loaded.LoadFromFile(path); err != nil {
		t.Fatal(err)
	}

	sub, _ := url.Parse("https://a.example.com/")
	if got := loaded.CookiesAsHeader(sub); strings.Contains(got, "hostonly") {
		t.Errorf("往返后 host-only 标记丢了: %q", got)
	}
	if got := loaded.CookiesAsHeader(u); !strings.Contains(got, "hostonly=1") || !strings.Contains(got, "shared=2") {
		t.Errorf("往返后 cookie 不全: %q", got)
	}
}
