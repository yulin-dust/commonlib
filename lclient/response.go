package lclient

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	curl "github.com/yulin-dust/commonlib/third_party/curlimpersonate"
	"golang.org/x/net/html/charset"
)

// Response 是统一的响应对象。
type Response struct {
	StatusCode int
	Status     string // "200 OK"
	Header     map[string][]string
	Body       []byte
	URL        string // 发起请求时的 URL（不含重定向）
	// FinalURL 是跟随完全部重定向后的最终 URL；未发生重定向时与 URL 相同。
	// 解析页面里的相对链接必须用它，用 URL 会把相对路径接到跳转前的地址上。
	FinalURL string
	Elapsed  time.Duration // 耗时
}

// OK 是否 2xx。
func (r *Response) OK() bool { return r.StatusCode >= 200 && r.StatusCode < 300 }

// Text 以字符串返回 body。
//
// Session 默认开启 WithAutoDecode，Body 进来时已按页面字符集转成 UTF-8，
// 因此直接用 Text() 即可，GB2312/GBK/Big5 等页面不会乱码。只有显式
// WithAutoDecode(false) 关掉之后，Body 才是服务器原始字节——那种情况下要
// 转码请用 DecodedText()。
func (r *Response) Text() string { return string(r.Body) }

// Bytes 返回 body 字节。与 Text() 同理：默认已是 UTF-8，
// 关闭 WithAutoDecode 后才是服务器原始字节。
func (r *Response) Bytes() []byte { return r.Body }

// DecodedBody 按页面声明的字符集把 body 转成 UTF-8 字节：依次看 Content-Type 头的
// charset、HTML <meta charset>、以及 BOM 嗅探。已是 UTF-8 或检测/转换失败时原样返回。
// 用于 GB2312/GBK/Big5/Shift-JIS 等非 UTF-8 页面，避免乱码。
//
// 仅在关闭了 WithAutoDecode 时才需要它——默认开启的情况下 Body 已经是 UTF-8，
// 再调一次是多余的（对已转好的内容通常无害，但没有意义）。
//
// 注意：若打算把 body 交给会自行检测字符集的解析器（如 webextract），请直接传原始
// Body，不要先 Decode——否则会按已过期的 <meta charset> 二次解码而损坏内容。
func (r *Response) DecodedBody() []byte {
	return decodeToUTF8(r.Body, r.HeaderGet("Content-Type"))
}

// DecodedText 是 DecodedBody 的字符串形式（UTF-8）。非 UTF-8 页面用它替代 Text()。
func (r *Response) DecodedText() string { return string(r.DecodedBody()) }

// decodeToUTF8 把 body 按检测到的字符集转成 UTF-8；已是 UTF-8 或失败时原样返回。
func decodeToUTF8(body []byte, contentType string) []byte {
	if len(body) == 0 {
		return body
	}
	enc, name, _ := charset.DetermineEncoding(body, contentType)
	if enc == nil || name == "utf-8" {
		return body
	}
	out, err := enc.NewDecoder().Bytes(body)
	if err != nil {
		return body
	}
	return out
}

// JSONUnmarshal 把 body 解析为 v。
func (r *Response) JSONUnmarshal(v any) error {
	return json.Unmarshal(r.Body, v)
}

// JSONMap 把 body 解析为 map[string]any。
func (r *Response) JSONMap() (map[string]any, error) {
	var m map[string]any
	err := json.Unmarshal(r.Body, &m)
	return m, err
}

// HeaderGet 取单个 header，不存在返回空。
func (r *Response) HeaderGet(key string) string {
	for k, v := range r.Header {
		if strings.EqualFold(k, key) && len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

// HeaderValues 取该 header 的所有值。
func (r *Response) HeaderValues(key string) []string {
	for k, v := range r.Header {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return nil
}

// Cookies 把响应里的 Set-Cookie 解析成 http.Cookie 列表。
func (r *Response) Cookies() []*http.Cookie {
	if r == nil || r.Header == nil {
		return nil
	}
	// 利用 http.Response 复用现有 cookie 解析逻辑
	dummy := &http.Response{Header: http.Header{}}
	for k, vs := range r.Header {
		for _, v := range vs {
			dummy.Header.Add(k, v)
		}
	}
	return dummy.Cookies()
}

// buildResponse 把底层 curl 返回的 Response 转成我们的对象。
func buildResponse(raw *curl.Response, reqURL string, elapsed time.Duration) *Response {
	finalURL := raw.EffectiveURL
	if finalURL == "" {
		finalURL = reqURL
	}
	r := &Response{
		StatusCode: raw.StatusCode,
		Body:       raw.Body,
		Header:     parseRawHeaders(raw.Headers),
		URL:        reqURL,
		FinalURL:   finalURL,
		Elapsed:    elapsed,
	}
	// 拼一个 status 文本
	if statusLine := r.HeaderGet("Status"); statusLine != "" {
		r.Status = statusLine
	} else {
		r.Status = strconv.Itoa(r.StatusCode) + " " + http.StatusText(r.StatusCode)
	}
	return r
}

// parseRawHeaders 把 curl 返回的原始 header 字符串解析成 map。
// raw 通常形如：
//
//	HTTP/2 200
//	content-type: text/html
//	set-cookie: a=1; Path=/
//	set-cookie: b=2; Path=/
//
// 注意 raw 里可能有**多段** header：curl 自动跟随重定向时每一跳各来一段，
// 1xx 中间响应、经代理时的 "HTTP/1.1 200 Connection established" 同理。只有
// 最后一段属于最终响应，因此每遇到一个状态行就重置累积结果——否则
// HeaderGet("Content-Type") 取到的会是第一跳（常是 302 页面）的值，进而让
// autoDecode 用错字符集去解码最终 body。
//
// Set-Cookie 是唯一的例外：每一跳下发的 cookie 都要进 jar，故跨段累积。
func parseRawHeaders(raw string) map[string][]string {
	out := make(map[string][]string)
	if raw == "" {
		return out
	}
	var setCookies []string // 跨所有段累积
	scanner := bufio.NewScanner(strings.NewReader(raw))
	// 初始 4KB（足够绝大多数 header 行），上限 1MB（容纳超大 Cookie 等）。
	// 初始值放小，避免每个响应都固定预分配 64KB。
	scanner.Buffer(make([]byte, 0, 4*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "HTTP/") {
			// 状态行：HTTP/2 200 或 HTTP/1.1 200 OK。新的一段开始，丢掉上一段。
			out = make(map[string][]string)
			parts := strings.SplitN(line, " ", 3)
			if len(parts) >= 2 {
				out["Status"] = []string{strings.TrimSpace(strings.Join(parts[1:], " "))}
			}
			continue
		}
		idx := strings.Index(line, ":")
		if idx <= 0 {
			continue
		}
		k := strings.TrimSpace(line[:idx])
		v := strings.TrimSpace(line[idx+1:])
		// 规范化 key（首字母大写）以兼容 http.Header 习惯
		k = http.CanonicalHeaderKey(k)
		if k == "Set-Cookie" {
			setCookies = append(setCookies, v)
			continue
		}
		out[k] = append(out[k], v)
	}
	if len(setCookies) > 0 {
		out["Set-Cookie"] = setCookies
	}
	return out
}
