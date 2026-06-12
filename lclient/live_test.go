package lclient

import (
	"os"
	"strings"
	"testing"
)

// 受 LCLIENT_LIVE=1 控制的真实网络测试。
// 验证每个内置身份的 Profile 都被当前 libcurl-impersonate 接受，且服务端回显的
// User-Agent 与该身份一致（端到端自洽）。
func TestLiveBuiltinIdentities(t *testing.T) {
	if os.Getenv("LCLIENT_LIVE") != "1" {
		t.Skip("set LCLIENT_LIVE=1 to run network test")
	}
	for _, id := range builtinIdentities {
		id := id
		t.Run(id.Name, func(t *testing.T) {
			s := NewSession(WithImpersonate(id.Profile))
			defer s.Close()
			resp, err := s.Get("https://postman-echo.com/headers", UseIdentity(id))
			if err != nil {
				t.Fatalf("%s (profile %s): %v", id.Name, id.Profile, err)
			}
			if resp.StatusCode != 200 {
				t.Skipf("echo 服务返回 %d，跳过断言", resp.StatusCode)
			}
			if !strings.Contains(resp.Text(), id.UserAgent) {
				t.Errorf("%s: 服务端未回显预期 UA\n  want %q", id.Name, id.UserAgent)
			}
		})
	}
}
