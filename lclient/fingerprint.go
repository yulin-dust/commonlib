package lclient

import (
	"encoding/json"
	"fmt"
)

// FingerprintReport 是 tls.peet.ws 返回的简化报告。
type FingerprintReport struct {
	JA3       string `json:"ja3"`
	JA3Hash   string `json:"ja3_hash"`
	JA4       string `json:"ja4"`
	JA4Hash   string `json:"ja4_hash"`
	Akamai    string `json:"akamai"`
	UserAgent string `json:"user_agent"`
	Raw       map[string]any
}

// CheckFingerprint 请求 tls.peet.ws/api/all 验证当前指纹。
// 适合在切换 profile 后做一次自检。
func (s *Session) CheckFingerprint() (*FingerprintReport, error) {
	return s.CheckFingerprintWith()
}

// CheckFingerprintWith 带上一组请求级选项（如 UseIdentity / Proxy / Impersonate）
// 抓一次指纹，用于核对"某个具体身份/代理下"实际呈现的 TLS / HTTP2 指纹。
func (s *Session) CheckFingerprintWith(opts ...RequestOption) (*FingerprintReport, error) {
	resp, err := s.Get("https://tls.peet.ws/api/all", opts...)
	if err != nil {
		return nil, err
	}
	if !resp.OK() {
		return nil, fmt.Errorf("fingerprint check failed: status %d", resp.StatusCode)
	}

	var raw map[string]any
	if err := resp.JSONUnmarshal(&raw); err != nil {
		return nil, err
	}

	r := &FingerprintReport{Raw: raw}

	// 尝试从常见字段提取，兼容 tls.peet.ws 不同版本结构
	if tls, ok := raw["tls"].(map[string]any); ok {
		r.JA3, _ = tls["ja3"].(string)
		r.JA3Hash, _ = tls["ja3_hash"].(string)
		r.JA4, _ = tls["ja4"].(string)
		r.JA4Hash, _ = tls["ja4_hash"].(string)
	}
	if h2, ok := raw["http2"].(map[string]any); ok {
		if a, ok := h2["akamai_fingerprint"].(string); ok {
			r.Akamai = a
		}
	}
	if ua, ok := raw["user_agent"].(string); ok {
		r.UserAgent = ua
	}

	return r, nil
}

// MarshalJSON 友好打印。
func (r *FingerprintReport) MarshalJSON() ([]byte, error) {
	type alias struct {
		JA3       string `json:"ja3,omitempty"`
		JA3Hash   string `json:"ja3_hash,omitempty"`
		JA4       string `json:"ja4,omitempty"`
		JA4Hash   string `json:"ja4_hash,omitempty"`
		Akamai    string `json:"akamai,omitempty"`
		UserAgent string `json:"user_agent,omitempty"`
	}
	return json.Marshal(alias{
		JA3: r.JA3, JA3Hash: r.JA3Hash,
		JA4: r.JA4, JA4Hash: r.JA4Hash,
		Akamai: r.Akamai, UserAgent: r.UserAgent,
	})
}
