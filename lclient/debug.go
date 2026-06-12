package lclient

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

type DebugLogger struct {
	mu       sync.Mutex
	out      io.Writer
	enabled  atomic.Bool
	maskKeys []string
	maxBody  int
}

func NewDebugLogger() *DebugLogger {
	d := &DebugLogger{
		out:      os.Stderr,
		maskKeys: []string{"Authorization", "Cookie", "Set-Cookie", "X-Api-Key", "X-Auth-Token"},
		maxBody:  4096,
	}
	return d
}

func (d *DebugLogger) Enable(on bool)  { d.enabled.Store(on) }
func (d *DebugLogger) IsEnabled() bool { return d.enabled.Load() }

func (d *DebugLogger) SetOutput(w io.Writer) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.out = w
}

func (d *DebugLogger) SetMaskKeys(keys ...string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.maskKeys = append(d.maskKeys, keys...)
}

func (d *DebugLogger) SetMaxBodyLength(n int) { d.maxBody = n }

func (d *DebugLogger) shouldMask(key string) bool {
	for _, k := range d.maskKeys {
		if strings.EqualFold(k, key) {
			return true
		}
	}
	return false
}

func (d *DebugLogger) logRequest(method, url string, headers OrderedHeaders, body []byte) {
	if !d.enabled.Load() {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	fmt.Fprintf(d.out, "\n>>> [lclient] %s %s\n", method, url)
	for _, kv := range headers {
		val := kv[1]
		if d.shouldMask(kv[0]) {
			val = maskValue(val)
		}
		fmt.Fprintf(d.out, ">>> %s: %s\n", kv[0], val)
	}
	if len(body) > 0 {
		fmt.Fprintf(d.out, ">>> Body: %s\n", truncate(string(body), d.maxBody))
	}
}

func (d *DebugLogger) logResponse(statusCode int, headers map[string][]string, body []byte, elapsed string) {
	if !d.enabled.Load() {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	fmt.Fprintf(d.out, "<<< [lclient] %d (%s)\n", statusCode, elapsed)
	for k, vs := range headers {
		for _, v := range vs {
			val := v
			if d.shouldMask(k) {
				val = maskValue(val)
			}
			fmt.Fprintf(d.out, "<<< %s: %s\n", k, val)
		}
	}
	if len(body) > 0 {
		fmt.Fprintf(d.out, "<<< Body: %s\n\n", truncate(string(body), d.maxBody))
	} else {
		fmt.Fprintln(d.out)
	}
}

func maskValue(v string) string {
	// 仅保留极少量首尾字符用于肉眼核对，避免在 debug 日志里泄露过多密钥内容。
	if len(v) <= 12 {
		return "***"
	}
	return v[:2] + "***" + v[len(v)-2:]
}

func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("... (%d bytes truncated)", len(s)-n)
}
