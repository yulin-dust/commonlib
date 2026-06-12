package lclient

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// ============================================================
// Params - URL 查询参数（支持中文，自动 encode）
// ============================================================

// Params 是 query string 参数。
// 使用 map 形式，顺序不重要（query string 顺序通常不参与指纹）。
type Params map[string]string

// Encode 将 Params 编码为 query string（已做 URL encode）。
func (p Params) Encode() string {
	if len(p) == 0 {
		return ""
	}
	v := url.Values{}
	for k, val := range p {
		v.Set(k, val)
	}
	return v.Encode()
}

// OrderedParams 当你需要 query 参数保序时使用（少数风控场景）。
type OrderedParams [][2]string

// Encode 将 OrderedParams 编码为 query string，保留顺序。
func (p OrderedParams) Encode() string {
	if len(p) == 0 {
		return ""
	}
	parts := make([]string, 0, len(p))
	for _, kv := range p {
		parts = append(parts, url.QueryEscape(kv[0])+"="+url.QueryEscape(kv[1]))
	}
	return strings.Join(parts, "&")
}

// ============================================================
// Headers - 请求头
// ============================================================

// Headers 是无序 headers，便于书写。
// 适合普通场景；高风控站请用 OrderedHeaders。
type Headers map[string]string

// OrderedHeaders 是有序 headers。
// 风控严格的网站（电商/Akamai/DataDome 等）会检测 header 顺序作为指纹的一部分。
// 推荐顺序参考真实浏览器抓包结果。
type OrderedHeaders [][2]string

// Add 追加一个 header（不去重，调用方控制）。
func (h *OrderedHeaders) Add(key, value string) {
	*h = append(*h, [2]string{key, value})
}

// Set 设置一个 header，存在则替换（保留原位置），不存在则追加。
func (h *OrderedHeaders) Set(key, value string) {
	for i, kv := range *h {
		if strings.EqualFold(kv[0], key) {
			(*h)[i][1] = value
			return
		}
	}
	*h = append(*h, [2]string{key, value})
}

// Get 取 header 值，不存在返回空字符串。大小写不敏感。
func (h OrderedHeaders) Get(key string) string {
	for _, kv := range h {
		if strings.EqualFold(kv[0], key) {
			return kv[1]
		}
	}
	return ""
}

// Has 判断 header 是否存在。
func (h OrderedHeaders) Has(key string) bool {
	for _, kv := range h {
		if strings.EqualFold(kv[0], key) {
			return true
		}
	}
	return false
}

// Delete 删除指定 header。
func (h *OrderedHeaders) Delete(key string) {
	for i, kv := range *h {
		if strings.EqualFold(kv[0], key) {
			*h = append((*h)[:i], (*h)[i+1:]...)
			return
		}
	}
}

// Clone 深拷贝。
func (h OrderedHeaders) Clone() OrderedHeaders {
	out := make(OrderedHeaders, len(h))
	copy(out, h)
	return out
}

// Merge 将一组 headers 合并到当前。
// 同名 key 替换值并保留原位置；新 key 追加到末尾。
// 这是 session 默认 headers 与请求级 headers 合并的标准规则。
func (h *OrderedHeaders) Merge(other OrderedHeaders) {
	for _, kv := range other {
		h.Set(kv[0], kv[1])
	}
}

// MergeMap 将无序 map 合并进来。
// 由于 map 无序，合并的 key 顺序不可预期；
// 一般用于追加少量请求级 header。
func (h *OrderedHeaders) MergeMap(m Headers) {
	for k, v := range m {
		h.Set(k, v)
	}
}

// Split 返回平行的 key / value 切片，保留原顺序。
// 用于把有序 header 传给底层（header 顺序是反爬指纹的一部分）。
func (h OrderedHeaders) Split() (keys, vals []string) {
	keys = make([]string, len(h))
	vals = make([]string, len(h))
	for i, kv := range h {
		keys[i] = kv[0]
		vals[i] = kv[1]
	}
	return keys, vals
}

// ToMap 转为 map（丢失顺序）。仅在底层 API 必须接收 map 时使用。
func (h OrderedHeaders) ToMap() map[string]string {
	m := make(map[string]string, len(h))
	for _, kv := range h {
		m[kv[0]] = kv[1]
	}
	return m
}

// ============================================================
// Body - 请求体
// ============================================================

// Body 是请求体抽象。
// 通过 JSON / Form / Raw / Multipart 等构造器创建。
type Body struct {
	data        string
	contentType string
	// raw 优先：如果直接给字节流（含 multipart），data 留空、raw 有值
	raw []byte
	// err 记录 body 构造期错误（如 JSON 序列化失败），在请求阶段返回。
	err error
}

// Bytes 返回 body 的字节内容（优先 raw）。
func (b *Body) Bytes() []byte {
	if b == nil {
		return nil
	}
	if b.raw != nil {
		return b.raw
	}
	return []byte(b.data)
}

// String 返回 body 字符串。
func (b *Body) String() string {
	return string(b.Bytes())
}

// ContentType 返回该 body 默认的 Content-Type。
func (b *Body) ContentType() string {
	if b == nil {
		return ""
	}
	return b.contentType
}

// JSON 构造 JSON body，自动序列化并设置 Content-Type。
// 序列化失败时不再伪造畸形 body，而是把错误带到请求阶段返回。
func JSON(v any) *Body {
	bs, err := json.Marshal(v)
	if err != nil {
		return &Body{err: fmt.Errorf("json marshal body: %w", err), contentType: "application/json; charset=utf-8"}
	}
	return &Body{data: string(bs), contentType: "application/json; charset=utf-8"}
}

// Form 构造 application/x-www-form-urlencoded body。
func Form(data map[string]string) *Body {
	v := url.Values{}
	for k, val := range data {
		v.Set(k, val)
	}
	return &Body{data: v.Encode(), contentType: "application/x-www-form-urlencoded"}
}

// OrderedForm 构造保序的 form-urlencoded body（部分风控站会校验字段顺序）。
func OrderedForm(data [][2]string) *Body {
	parts := make([]string, 0, len(data))
	for _, kv := range data {
		parts = append(parts, url.QueryEscape(kv[0])+"="+url.QueryEscape(kv[1]))
	}
	return &Body{data: strings.Join(parts, "&"), contentType: "application/x-www-form-urlencoded"}
}

// Raw 用原始字符串作为 body。
// 若 contentType 为空，默认 application/octet-stream。
func Raw(s string, contentType string) *Body {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return &Body{data: s, contentType: contentType}
}

// RawBytes 用字节切片作为 body。
func RawBytes(b []byte, contentType string) *Body {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return &Body{raw: b, contentType: contentType}
}
