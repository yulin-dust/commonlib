package lclient

import "testing"

func TestNormalizeProxyForCurl(t *testing.T) {
	cases := []struct {
		in       string
		wantURL  string
		wantConv bool
	}{
		{"", "", false},
		{"http://x:y@host:1234", "http://x:y@host:1234", false},
		{"https://x:y@host:1234", "http://x:y@host:1234", true},
		{"HTTPS://x:y@host:1234", "http://x:y@host:1234", true}, // 大小写不敏感
		{"socks5://u:p@h:1080", "socks5://u:p@h:1080", false},
		{"socks5h://u:p@h:1080", "socks5h://u:p@h:1080", false},
		{"https://7054F315:1850F5FB7565@tun-vdpzuj.qg.net:13604",
			"http://7054F315:1850F5FB7565@tun-vdpzuj.qg.net:13604", true},
	}
	for _, c := range cases {
		got, conv := normalizeProxyForCurl(c.in)
		if got != c.wantURL || conv != c.wantConv {
			t.Errorf("normalizeProxyForCurl(%q):\n  got  (%q, %v)\n  want (%q, %v)",
				c.in, got, conv, c.wantURL, c.wantConv)
		}
	}
}
