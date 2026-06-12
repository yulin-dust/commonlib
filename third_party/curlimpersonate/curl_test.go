package curlimpersonate

// cgo 层独立单测：全部打本地 httptest 服务，离线、确定性。
// 验证一次性句柄 / 可复用句柄 / 共享缓存 / SSRF 拦截 / 协议限制 / 响应体闸 /
// 几何扩容缓冲 / context 取消 / 代理 URL 解析等。

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// req 构造一个打到本地服务、默认校验 TLS 关闭（httptest HTTP 无 TLS）的请求。
func req(url string) Request {
	return Request{
		URL:         url,
		Impersonate: "chrome131",
		Method:      "GET",
		TimeoutSec:  10,
		VerifyTLS:   false,
	}
}

func TestDo_BasicGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "hello")
		_, _ = w.Write([]byte("body-" + r.Method))
	}))
	defer srv.Close()

	resp, err := Do(req(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if string(resp.Body) != "body-GET" {
		t.Fatalf("body=%q", resp.Body)
	}
	if !strings.Contains(strings.ToLower(resp.Headers), "x-test: hello") {
		t.Errorf("响应头未解析到 X-Test:\n%s", resp.Headers)
	}
}

func TestDo_PostBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("方法应为 POST，实际 %s", r.Method)
		}
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		_, _ = w.Write(b) // 回显
	}))
	defer srv.Close()

	r := req(srv.URL)
	r.Method = "POST"
	r.Body = []byte("hello=世界")
	resp, err := Do(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Body) != "hello=世界" {
		t.Fatalf("回显不符: %q", resp.Body)
	}
}

// 二进制安全：含 NUL 的 body 不应被截断。
func TestDo_BinaryBody(t *testing.T) {
	want := []byte{0x00, 0x01, 0x00, 0xff, 0x00}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		_, _ = w.Write(b)
	}))
	defer srv.Close()

	r := req(srv.URL)
	r.Method = "POST"
	r.Body = want
	resp, err := Do(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(resp.Body, want) {
		t.Fatalf("二进制 body 被破坏: %v", resp.Body)
	}
}

// 几何扩容缓冲：大响应应完整无损返回。
func TestDo_LargeBodyIntact(t *testing.T) {
	const n = 3 * 1024 * 1024 // 3 MiB
	payload := bytes.Repeat([]byte("abcdefghij"), n/10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	resp, err := Do(req(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Body) != len(payload) {
		t.Fatalf("长度不符: got %d want %d", len(resp.Body), len(payload))
	}
	if !bytes.Equal(resp.Body, payload) {
		t.Fatal("大响应内容被破坏")
	}
}

func TestDo_MaxBodyBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 64*1024))
	}))
	defer srv.Close()

	r := req(srv.URL)
	r.MaxBodyBytes = 1024
	_, err := Do(r)
	if err == nil || !strings.Contains(err.Error(), "exceeds max size limit") {
		t.Fatalf("期望超限错误，得到 %v", err)
	}

	// 上限足够时应成功。
	r.MaxBodyBytes = 1 << 20
	resp, err := Do(r)
	if err != nil || len(resp.Body) != 64*1024 {
		t.Fatalf("上限足够时应成功: err=%v len=%d", err, len(resp.Body))
	}
}

func TestDo_FileProtocolBlocked(t *testing.T) {
	r := req("file:///etc/hosts")
	_, err := Do(r)
	if err == nil {
		t.Fatal("file:// 应被协议限制拒绝")
	}
}

func TestDo_BlockPrivateIPs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close() // 监听 127.0.0.1

	// 不拦截：本地服务可达。
	if _, err := Do(req(srv.URL)); err != nil {
		t.Fatalf("未开拦截时应可达: %v", err)
	}

	// 开拦截：127.0.0.1 应被 SSRF 防护中止。
	r := req(srv.URL)
	r.BlockPrivateIPs = true
	_, err := Do(r)
	if err == nil || !strings.Contains(err.Error(), "blocked private or loopback") {
		t.Fatalf("期望 SSRF 拦截，得到 %v", err)
	}
}

func TestHandle_Reuse(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	h, err := NewHandle()
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	for i := 0; i < 3; i++ {
		resp, err := h.Do(req(srv.URL))
		if err != nil || resp.StatusCode != 200 || string(resp.Body) != "ok" {
			t.Fatalf("第 %d 次复用失败: err=%v", i, err)
		}
	}
	if hits.Load() != 3 {
		t.Fatalf("期望 3 次命中，实际 %d", hits.Load())
	}

	// 关闭后再用应报错。
	h.Close()
	if _, err := h.Do(req(srv.URL)); err == nil {
		t.Error("已关闭句柄应拒绝使用")
	}
}

// 共享缓存并发：多 goroutine 共用一个 Share，靠 C 端锁回调保护，-race 验证。
func TestShare_Concurrent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	sh, err := NewShare()
	if err != nil {
		t.Fatal(err)
	}
	defer sh.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 30)
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := req(srv.URL)
			r.Share = sh
			if resp, err := Do(r); err != nil {
				errs <- err
			} else if resp.StatusCode != 200 {
				errs <- &statusErr{resp.StatusCode}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

type statusErr struct{ code int }

func (e *statusErr) Error() string { return "unexpected status" }

func TestContextCancel(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(block)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(150 * time.Millisecond); cancel() }()

	r := req(srv.URL)
	r.TimeoutSec = 30
	r.Ctx = ctx
	start := time.Now()
	_, err := Do(r)
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("取消应迅速中止在途请求: err=%v elapsed=%v", err, time.Since(start))
	}
	if !strings.Contains(err.Error(), "canceled by context") {
		t.Errorf("错误信息应表明被取消: %v", err)
	}
}

func TestHeaderLengthMismatch(t *testing.T) {
	r := req("http://example.com")
	r.HeaderKeys = []string{"A", "B"}
	r.HeaderVals = []string{"1"} // 数量不匹配
	if _, err := Do(r); err == nil || !strings.Contains(err.Error(), "length mismatch") {
		t.Fatalf("期望长度不匹配错误，得到 %v", err)
	}
}

func TestParseProxyURL(t *testing.T) {
	cases := []struct {
		in, wantAddr, wantAuth string
	}{
		{"", "", ""},
		{"http://host:8080", "http://host:8080", ""},
		{"http://user:pass@host:8080", "http://host:8080", "user:pass"},
		{"socks5h://u:p@h:1080", "socks5h://h:1080", "u:p"},
		{"host:8080", "host:8080", ""},
	}
	for _, c := range cases {
		addr, auth := parseProxyURL(c.in)
		if addr != c.wantAddr || auth != c.wantAuth {
			t.Errorf("parseProxyURL(%q) = (%q,%q)，期望 (%q,%q)", c.in, addr, auth, c.wantAddr, c.wantAuth)
		}
	}
}

// 自定义 header 应原样送达（值含中文 / 较长也不被截断）。
func TestDo_CustomHeaders(t *testing.T) {
	long := strings.Repeat("x", 8000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-One") != "中文值" || r.Header.Get("X-Long") != long {
			t.Errorf("自定义 header 未原样送达: one=%q long.len=%d", r.Header.Get("X-One"), len(r.Header.Get("X-Long")))
		}
		w.WriteHeader(204)
	}))
	defer srv.Close()

	r := req(srv.URL)
	r.HeaderKeys = []string{"X-One", "X-Long"}
	r.HeaderVals = []string{"中文值", long}
	resp, err := Do(r)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	if resp.StatusCode != 204 {
		t.Fatalf("status=%d，期望 204", resp.StatusCode)
	}
}
