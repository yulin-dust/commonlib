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
	URL        string        // 请求的 URL（可能不是最终重定向后的）
	Elapsed    time.Duration // 耗时
}

// OK 是否 2xx。
func (r *Response) OK() bool { return r.StatusCode >= 200 && r.StatusCode < 300 }

// Text 以字符串返回 body（原始字节，不做字符集转换）。
// 目标页是 GB2312/GBK/Big5 等非 UTF-8 编码时，这里会乱码——改用 DecodedText()。
func (r *Response) Text() string { return string(r.Body) }

// Bytes 返回 body 字节（原始）。
func (r *Response) Bytes() []byte { return r.Body }

// DecodedBody 按页面声明的字符集把 body 转成 UTF-8 字节：依次看 Content-Type 头的
// charset、HTML <meta charset>、以及 BOM 嗅探。已是 UTF-8 或检测/转换失败时原样返回。
// 用于 GB2312/GBK/Big5/Shift-JIS 等非 UTF-8 页面，避免乱码。
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
	r := &Response{
		StatusCode: raw.StatusCode,
		Body:       raw.Body,
		Header:     parseRawHeaders(raw.Headers),
		URL:        reqURL,
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
func parseRawHeaders(raw string) map[string][]string {
	out := make(map[string][]string)
	if raw == "" {
		return out
	}
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
			// 状态行：HTTP/2 200 或 HTTP/1.1 200 OK
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
		out[k] = append(out[k], v)
	}
	return out
}
