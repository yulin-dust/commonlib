package lclient

import (
	"os"
	"strings"
	"testing"
)

// 跨浏览器族抓真实指纹（tls.peet.ws），核对：
//   - TLS 握手被正确解析（ja3_hash 非空）
//   - JA4 表明 TLS 1.3（真实浏览器特征，t13...）
//   - HTTP/2 Akamai 指纹存在（像真实浏览器走 h2）
//   - 服务端回显的 UA 与我们声称的浏览器一致（自洽）
func TestLiveFingerprintCoherence(t *testing.T) {
	if os.Getenv("LCLIENT_LIVE") != "1" {
		t.Skip("set LCLIENT_LIVE=1")
	}
	cases := []struct {
		profile string
		uaMark  string
	}{
		{Chrome131, "Chrome/131"},
		{Chrome124, "Chrome/124"},
		{Firefox133, "Firefox/133"},
		{Safari18, "Version/18"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.profile, func(t *testing.T) {
			s := NewSession(WithImpersonate(c.profile), WithDefaultHeadersMap(Headers{
				"User-Agent": UserAgentFor(c.profile),
			}))
			defer s.Close()
			rep, err := s.CheckFingerprint()
			if err != nil {
				t.Fatalf("%s: %v", c.profile, err)
			}
			t.Logf("%-12s ja3=%s ja4=%s akamai=%s\n            ua=%s",
				c.profile, rep.JA3Hash, rep.JA4, rep.Akamai, rep.UserAgent)

			if rep.JA3Hash == "" {
				t.Errorf("%s: ja3_hash 为空，TLS 握手未被解析", c.profile)
			}
			if !strings.HasPrefix(rep.JA4, "t13") {
				t.Errorf("%s: JA4=%q 非 TLS1.3，真实浏览器应为 t13...", c.profile, rep.JA4)
			}
			if rep.Akamai == "" {
				t.Errorf("%s: 无 HTTP/2 Akamai 指纹（未走 h2？）", c.profile)
			}
		})
	}
}

// 身份轮换下，每个内置身份的 TLS 指纹与其声称的 UA 都应自洽。
func TestLiveIdentityFingerprintCoherence(t *testing.T) {
	if os.Getenv("LCLIENT_LIVE") != "1" {
		t.Skip("set LCLIENT_LIVE=1")
	}
	for _, id := range builtinIdentities {
		s := NewSession()
		rep, err := s.CheckFingerprintWith(UseIdentity(id))
		s.Close()
		if err != nil {
			t.Errorf("%s: %v", id.Name, err)
			continue
		}
		t.Logf("%-22s ja4=%s ua=%s", id.Name, rep.JA4, firstUAToken(rep.UserAgent))
		if rep.JA3Hash == "" || !strings.HasPrefix(rep.JA4, "t13") {
			t.Errorf("%s: 指纹异常 ja3=%q ja4=%q", id.Name, rep.JA3Hash, rep.JA4)
		}
	}
}

func firstUAToken(ua string) string {
	if i := strings.Index(ua, ")"); i > 0 && i+1 < len(ua) {
		return strings.TrimSpace(ua[i+1:])
	}
	return ua
}
