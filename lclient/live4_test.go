package lclient

import (
	"os"
	"testing"
)

// 开启 SSRF 防护后，正常公网站点仍应可访问（不能误杀公网 IP）。
func TestLiveBlockPrivateNoFalsePositive(t *testing.T) {
	if os.Getenv("LCLIENT_LIVE") != "1" {
		t.Skip("set LCLIENT_LIVE=1")
	}
	s := NewSession(WithBlockPrivateIPs(true), WithConnectionPool(2))
	defer s.Close()
	resp, err := s.Get("https://postman-echo.com/get")
	if err != nil {
		t.Fatalf("公网站点被误杀: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("期望 200，得到 %d", resp.StatusCode)
	}
	t.Logf("公网访问正常（防护开启 + 连接池），status=%d", resp.StatusCode)
}
