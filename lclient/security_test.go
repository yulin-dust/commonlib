package lclient

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// CRLF header 注入应被拒绝。
func TestValidateHeadersRejectsCRLF(t *testing.T) {
	cases := []OrderedHeaders{
		{{"X-Evil", "ok\r\nInjected: 1"}}, // 值含 CRLF
		{{"X-Evil", "ok\nInjected: 1"}},   // 值含 LF
		{{"X-Ev\r\nil", "v"}},             // 键含 CRLF
		{{"", "v"}},                       // 空键
		{{"X-Bell", "a\x00b"}},            // 值含 NUL
	}
	for i, h := range cases {
		if err := validateHeaders(h); !errors.Is(err, ErrInvalidHeader) {
			t.Errorf("case %d: 期望 ErrInvalidHeader，得到 %v", i, err)
		}
	}
	// 合法 header（含制表符）应放行。
	if err := validateHeaders(OrderedHeaders{{"User-Agent", "x"}, {"X-Tab", "a\tb"}}); err != nil {
		t.Errorf("合法 header 不应报错: %v", err)
	}
}

// 端到端：含 CRLF 的请求级 header 应在发送前被拦截。
func TestRequestRejectsInjectedHeader(t *testing.T) {
	s := NewSession()
	defer s.Close()
	_, err := s.Get("https://example.com", Headers{"X-Evil": "v\r\nHost: evil.com"})
	if !errors.Is(err, ErrInvalidHeader) {
		t.Fatalf("期望 ErrInvalidHeader，得到 %v", err)
	}
}

// 协议限制：file:// 应被拒绝（防本地文件读取）。无需联网。
func TestFileProtocolBlocked(t *testing.T) {
	s := NewSession()
	defer s.Close()
	resp, err := s.Get("file:///etc/hosts")
	if err == nil {
		t.Fatalf("file:// 本应被拒绝，却成功了，body=%q", snippetSec(resp))
	}
	t.Logf("file:// 正确被拒: %v", err)
}

// SSRF：对解析到 127.0.0.1 的目标，开启 WithBlockPrivateIPs 应拦截；不开启则放行。
// 用本地 httptest 服务（监听 127.0.0.1），完全离线、确定性。
func TestSSRFBlockLocalhost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	// 不开防护：应能连通本地服务。
	s1 := NewSession()
	defer s1.Close()
	resp, err := s1.Get(srv.URL)
	if err != nil {
		t.Fatalf("未开防护时应能访问本地服务: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("期望 200，得到 %d", resp.StatusCode)
	}

	// 开启防护：访问 127.0.0.1 应被 SSRF 拦截。
	s2 := NewSession(WithBlockPrivateIPs(true))
	defer s2.Close()
	_, err = s2.Get(srv.URL)
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("期望 ErrBlockedAddress，得到 %v", err)
	}
	t.Logf("SSRF 拦截生效: %v", err)
}

func snippetSec(r *Response) string {
	if r == nil {
		return ""
	}
	s := strings.ReplaceAll(r.Text(), "\n", " ")
	if len(s) > 60 {
		return s[:60]
	}
	return s
}
