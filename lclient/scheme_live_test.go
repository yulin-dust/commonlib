package lclient

import (
	"os"
	"strings"
	"testing"
)

// 非安全上下文（普通 http，非 localhost）经 apply 套用身份后，不应带 sec-ch-ua* / priority。
// 走 apply 而非真实请求，避免 httptest 的 127.0.0.1 被判为安全上下文。
func TestSchemeAwareApplyDropsClientHintsOnHTTP(t *testing.T) {
	r := newIdentityRotator(WithIdentityPool(builtinIdentities[0])) // Chrome 131 Win
	prep := &PreparedRequest{URL: "http://example.com/path"}        // 明文 http、非 localhost
	r.apply(prep)
	for _, k := range []string{"sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "priority"} {
		if prep.Headers.Has(k) {
			t.Errorf("http 上下文不应带 %q", k)
		}
	}
	if !strings.Contains(prep.Headers.Get("User-Agent"), "Chrome/131") || prep.Headers.Get("Sec-Fetch-Mode") == "" {
		t.Error("http 上下文仍应带 UA 与 Sec-Fetch-*")
	}

	// 对照：https 上同一身份应带客户端提示与 priority。
	prep2 := &PreparedRequest{URL: "https://example.com/path"}
	r.apply(prep2)
	if prep2.Headers.Get("sec-ch-ua") == "" || prep2.Headers.Get("priority") == "" {
		t.Error("https 上下文应带 sec-ch-ua 与 priority")
	}
}

// 真实 https 站点（安全上下文）：应带 sec-ch-ua 与 priority。
func TestSchemeAwareHTTPSSendsClientHints(t *testing.T) {
	if os.Getenv("LCLIENT_LIVE") != "1" {
		t.Skip("set LCLIENT_LIVE=1")
	}
	s := NewSession(WithRandomIdentity(WithIdentityPool(builtinIdentities[0]))) // Chrome 131 Win
	defer s.Close()
	resp, err := s.Get("https://postman-echo.com/headers")
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ToLower(resp.Text())
	if !strings.Contains(body, `"sec-ch-ua"`) || !strings.Contains(body, `"priority"`) {
		t.Errorf("https 上下文应带 sec-ch-ua 与 priority:\n%s", resp.Text())
	}
}
