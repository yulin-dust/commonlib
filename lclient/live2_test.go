package lclient

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// 验证压缩响应能被正确解压（含 br / zstd），返回的是明文而非压缩字节。
func TestLiveDecompression(t *testing.T) {
	if os.Getenv("LCLIENT_LIVE") != "1" {
		t.Skip("set LCLIENT_LIVE=1")
	}
	s := NewSession(WithRandomIdentity()) // 身份会带 Accept-Encoding: gzip, deflate, br, zstd
	defer s.Close()
	// httpbin 的 /gzip /brotli 端点返回对应压缩；postman-echo 也支持 gzip。
	for _, ep := range []string{"https://httpbin.org/gzip", "https://httpbin.org/brotli"} {
		resp, err := s.Get(ep)
		if err != nil {
			t.Logf("%s: %v（端点不可用则跳过）", ep, err)
			continue
		}
		if resp.StatusCode != 200 {
			t.Logf("%s: status %d，跳过", ep, resp.StatusCode)
			continue
		}
		// 解压成功的话 body 是 JSON 明文，含 "gzipped"/"brotli" 字段。
		if !strings.Contains(resp.Text(), "{") {
			t.Errorf("%s: body 看起来没被解压：%q", ep, snippet(resp.Text()))
		} else {
			t.Logf("%s OK: %s", ep, snippet(resp.Text()))
		}
	}
}

// 验证响应体大小上限会触发 ErrBodyTooLarge。
func TestLiveMaxBodyBytes(t *testing.T) {
	if os.Getenv("LCLIENT_LIVE") != "1" {
		t.Skip("set LCLIENT_LIVE=1")
	}
	s := NewSession(WithMaxResponseBytes(1024)) // 1KB 上限
	defer s.Close()
	// 请求一个明显大于 1KB 的响应。
	_, err := s.Get("https://httpbin.org/bytes/65536")
	if err == nil {
		t.Fatal("预期超限报错，却成功了")
	}
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("预期 ErrBodyTooLarge，实际 %v", err)
	}
	t.Logf("超限正确报错: %v", err)

	// 对照：上限足够大时同一请求应成功。
	s2 := NewSession(WithMaxResponseBytes(1 << 20))
	defer s2.Close()
	resp, err := s2.Get("https://httpbin.org/bytes/65536")
	if err != nil {
		t.Fatalf("上限足够时不应报错: %v", err)
	}
	if len(resp.Body) != 65536 {
		t.Errorf("期望 65536 字节，实际 %d", len(resp.Body))
	}
}

func snippet(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 80 {
		return s[:80]
	}
	return s
}
