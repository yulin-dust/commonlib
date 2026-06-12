package webextract

import (
	"hash/fnv"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// --- selection / node 辅助函数 ---

// nodeName 返回元素的小写标签名；非元素节点（或空 selection）返回空串。
func nodeName(s *goquery.Selection) string {
	if len(s.Nodes) == 0 || s.Nodes[0].Type != html.ElementNode {
		return ""
	}
	return strings.ToLower(s.Nodes[0].Data)
}

// attr 返回属性值（不存在时返回空串），省去到处写 _ 的样板。
func attr(s *goquery.Selection, name string) string {
	v, _ := s.Attr(name)
	return v
}

// htmlOf 返回 selection 的内部 HTML（已 TrimSpace），出错时返回空串。
func htmlOf(s *goquery.Selection) string {
	h, err := s.Html()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(h)
}

// newElement 按标签名新建一个元素节点（同时填好 DataAtom）。
func newElement(tag string) *html.Node {
	return &html.Node{Type: html.ElementNode, Data: tag, DataAtom: atom.Lookup([]byte(tag))}
}

// appendClone 把 sel 底层节点深拷贝后挂到 parent 下。
func appendClone(parent *html.Node, sel *goquery.Selection) {
	if len(sel.Nodes) == 0 {
		return
	}
	parent.AppendChild(cloneNode(sel.Nodes[0]))
}

// cloneNode 递归深拷贝一个 html 节点（含属性与全部子树）。
func cloneNode(n *html.Node) *html.Node {
	clone := &html.Node{
		Type:     n.Type,
		DataAtom: n.DataAtom,
		Data:     n.Data,
		Attr:     append([]html.Attribute(nil), n.Attr...),
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		clone.AppendChild(cloneNode(c))
	}
	return clone
}

// --- 文本度量 ---

// lenRunes 返回字符串的 rune 数（而非字节数），中文计数才准确。
func lenRunes(s string) int { return utf8.RuneCountInString(s) }

// directTextLen 统计“直接挂在该节点下的文本”的 rune 数（不含嵌套元素里的文本）——
// 用作“这个容器是否真的装着散文”的近似信号。
func directTextLen(s *goquery.Selection) int {
	if len(s.Nodes) == 0 {
		return 0
	}
	n := 0
	for c := s.Nodes[0].FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			n += lenRunes(strings.TrimSpace(c.Data))
		}
	}
	return n
}

// selNodes 返回 selection 底层的 html 节点切片（拷贝一份，避免别名问题）。
func selNodes(s *goquery.Selection) []*html.Node {
	return append([]*html.Node(nil), s.Nodes...)
}

// punctCount 统计“句内标点”（中英文皆计），用作“这段读起来像散文”的近似信号。
func punctCount(s string) int {
	n := 0
	for _, r := range s {
		switch r {
		case ',', '，', '、', '；', ';', '。', '!', '！', '?', '？', '：', ':':
			n++
		}
	}
	return n
}

// linkDensity 返回“位于 <a> 标签内的字符”占全部字符的比例，是判别导航/标签云
// 等链接密集区的关键信号。文本为空时返回 0。
func linkDensity(s *goquery.Selection) float64 {
	total := lenRunes(strings.TrimSpace(s.Text()))
	if total == 0 {
		return 0
	}
	var linkLen int
	s.Find("a").Each(func(_ int, a *goquery.Selection) {
		linkLen += lenRunes(strings.TrimSpace(a.Text()))
	})
	return float64(linkLen) / float64(total)
}

var reWhitespace = regexp.MustCompile(`[ \t]+`) // 行内连续空白
var reBlankLines = regexp.MustCompile(`\n{3,}`) // 三个及以上换行

// collapseSpaces 把行内连续空白压成单个空格并 TrimSpace。
func collapseSpaces(s string) string {
	return strings.TrimSpace(reWhitespace.ReplaceAllString(s, " "))
}

// nodeText 把正文节点渲染成易读的纯文本：在块级元素之间保留段落换行，
// 行内空白被压缩，多余空行被并成一个空行（段落以 "\n\n" 分隔）。
func nodeText(s *goquery.Selection) string {
	if len(s.Nodes) == 0 {
		return ""
	}
	var b strings.Builder
	for _, n := range s.Nodes {
		writeText(&b, n)
	}
	out := b.String()
	// 规范化空白：逐行压缩行内空白，再把连续空行收敛成单个空行。
	lines := strings.Split(out, "\n")
	for i, ln := range lines {
		lines[i] = collapseSpaces(ln)
	}
	out = strings.Join(lines, "\n")
	out = reBlankLines.ReplaceAllString(out, "\n\n")
	return strings.TrimSpace(out)
}

// blockTags 是渲染纯文本时需要在前后补换行的块级标签集合。
var blockTags = map[string]bool{
	"p": true, "div": true, "br": true, "li": true, "tr": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"blockquote": true, "pre": true, "section": true, "article": true,
	"figure": true, "figcaption": true, "ul": true, "ol": true,
}

// writeText 递归把节点的可见文本写入 b：文本节点原样写出，<br> 转为换行，
// 其它块级元素在内容前后补换行以保留段落感。
func writeText(b *strings.Builder, n *html.Node) {
	switch n.Type {
	case html.TextNode:
		b.WriteString(n.Data)
	case html.ElementNode:
		name := strings.ToLower(n.Data)
		if name == "br" {
			b.WriteString("\n")
			return
		}
		block := blockTags[name]
		if block {
			b.WriteString("\n")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			writeText(b, c)
		}
		if block {
			b.WriteString("\n")
		}
	default:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			writeText(b, c)
		}
	}
}

// countWords 计算粗略字数：CJK 字符逐字计数，其余按“空白分隔的词”计数——
// 这样在中英文混排下都能给出合理的长度信号。
func countWords(s string) int {
	count := 0
	inWord := false
	for _, r := range s {
		if isCJK(r) {
			count++
			inWord = false
			continue
		}
		if unicode.IsSpace(r) {
			inWord = false
			continue
		}
		if !inWord {
			count++
			inWord = true
		}
	}
	return count
}

// isCJK 判断字符是否属于中日韩文字（汉字/平假名/片假名/谚文）。
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

// truncateRunes 把多行文本拍平成单行并按 rune 截断到 n 个字符，超长时补省略号。
func truncateRunes(s string, n int) string {
	s = collapseSpaces(strings.ReplaceAll(s, "\n", " "))
	if lenRunes(s) <= n {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:n])) + "…"
}

// --- 标题清理 ---

// titleSep 匹配标题中常见的分隔符（前后带空格的 | - – — » · :）。
var titleSep = regexp.MustCompile(`\s+[|\-–—»·:]\s+`)

// cleanTitle 去掉标题尾部的“ - 站点名”之类后缀，尽量只留下文章标题本身。
//
// 处理顺序：
//  1. 已知站点名时，优先剥掉 " | 站点名" / " - 站点名" 等精确后缀；
//  2. 否则按通用分隔符切分，若首段足够长（≥10 字）就取首段；
//  3. 最后兜底处理中文站点常见的无空格尾缀，如 "标题_某某网"、"标题｜某某网"。
func cleanTitle(title, site string) string {
	title = collapseSpaces(title)
	if title == "" {
		return ""
	}
	if site != "" {
		for _, suffix := range []string{" | " + site, " - " + site, " — " + site, " – " + site} {
			if strings.HasSuffix(title, suffix) {
				return strings.TrimSpace(strings.TrimSuffix(title, suffix))
			}
		}
	}
	// 按分隔符切分后，若首段够长则保留首段。
	if parts := titleSep.Split(title, -1); len(parts) > 1 {
		head := strings.TrimSpace(parts[0])
		if lenRunes(head) >= 10 {
			return head
		}
	}
	// 兜底剥掉无空格的 " _ 站点名" / "｜站点名" / "|站点名" 尾缀（中文站点常见，
	// 如 "标题_某某网"）：要求标题主体 ≥8 字、尾部 1~20 字，避免误切正文。
	for _, sep := range []string{"_", "｜", "|", " - ", " — "} {
		if i := strings.LastIndex(title, sep); i > 0 {
			head := strings.TrimSpace(title[:i])
			tail := strings.TrimSpace(title[i+len(sep):])
			if lenRunes(head) >= 8 && lenRunes(tail) > 0 && lenRunes(tail) <= 20 {
				return head
			}
		}
	}
	return title
}

// --- url / 图片 辅助函数 ---

// absURL 基于 base 把相对地址 ref 解析成绝对地址；base 为空或解析失败时原样返回。
func absURL(ref string, base *url.URL) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || base == nil {
		return ref
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return base.ResolveReference(u).String()
}

// bestImageSrc 在“真实图片地址”优先于“懒加载占位”的前提下挑出最佳 <img> 源：
// 依次尝试 src / data-src / data-original / data-lazy-src（跳过 data:image 内联占位），
// 都没有时退回 srcset 的第一个候选。
func bestImageSrc(s *goquery.Selection) string {
	for _, a := range []string{"src", "data-src", "data-original", "data-lazy-src"} {
		if v, ok := s.Attr(a); ok && strings.TrimSpace(v) != "" && !strings.HasPrefix(v, "data:image") {
			return strings.TrimSpace(v)
		}
	}
	if v, ok := s.Attr("srcset"); ok {
		// srcset 形如 "url1 1x, url2 2x"，取第一个候选的 URL 部分。
		if first := strings.SplitN(strings.TrimSpace(v), " ", 2)[0]; first != "" {
			return first
		}
	}
	return ""
}

// absolutizeImages 把 HTML 片段里相对的 img src 与 a href 改写成绝对地址。
func absolutizeImages(fragment string, base *url.URL) string {
	if base == nil || fragment == "" {
		return fragment
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(fragment))
	if err != nil {
		return fragment
	}
	doc.Find("img").Each(func(_ int, s *goquery.Selection) {
		if src := bestImageSrc(s); src != "" {
			s.SetAttr("src", absURL(src, base))
		}
	})
	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		if href, ok := s.Attr("href"); ok {
			s.SetAttr("href", absURL(href, base))
		}
	})
	return htmlOf(doc.Find("body"))
}

// --- 杂项 ---

// appendUnique 把非空且未出现过的 v 追加进 list（保持插入顺序去重）。
func appendUnique(list []string, v string) []string {
	if v == "" {
		return list
	}
	for _, e := range list {
		if e == v {
			return list
		}
	}
	return append(list, v)
}

// firstNonEmpty 返回第一个去空白后非空的字符串，全空则返回空串。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// hostnameOf 从 URL 提取主机名（去端口、去前导 "www."）。优先解析 rawURL，
// 失败时退回 base。都拿不到时返回空串。
func hostnameOf(rawURL string, base *url.URL) string {
	host := ""
	if u, err := url.Parse(strings.TrimSpace(rawURL)); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	} else if base != nil {
		host = base.Hostname()
	}
	host = strings.ToLower(strings.TrimSpace(host))
	return strings.TrimPrefix(host, "www.")
}

// reURLDate 匹配 URL 路径里的日期：/2026/05/30/ 或 /2026-05-30 等。
var reURLDate = regexp.MustCompile(`/(20\d{2})[-/](\d{1,2})[-/](\d{1,2})(?:[-/]|$)`)

// dateFromURL 尝试从 URL 路径解析发布日期（如 /news/2026/05/30/...）。
// 这是 trafilatura 也采用的兜底思路；月/日越界则视为无效。
func dateFromURL(rawURL string) time.Time {
	m := reURLDate.FindStringSubmatch(rawURL)
	if m == nil {
		return time.Time{}
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	if mo < 1 || mo > 12 || d < 1 || d > 31 {
		return time.Time{}
	}
	return time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
}

// reCCLicense 从 Creative Commons 链接里提取许可代号与版本，如
// creativecommons.org/licenses/by-sa/4.0/ → by-sa, 4.0。
var reCCLicense = regexp.MustCompile(`(?i)creativecommons\.org/licenses/([a-z-]+)/(\d+\.\d+)`)

// ccLicenseName 把 CC 许可链接转成可读名，如 "CC BY-SA 4.0"；非 CC 链接返回空串。
func ccLicenseName(href string) string {
	m := reCCLicense.FindStringSubmatch(href)
	if m == nil {
		return ""
	}
	return "CC " + strings.ToUpper(m[1]) + " " + m[2]
}

// contentFingerprint 计算正文的内容指纹：归一化（去空白、转小写）后取 FNV-1a 哈希，
// 输出 16 位十六进制。用于跨页面识别重复正文（站点级样板、转载等）。
func contentFingerprint(text string) string {
	norm := strings.ToLower(strings.Join(strings.Fields(text), " "))
	if norm == "" {
		return ""
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(norm))
	return strconv.FormatUint(h.Sum64(), 16)
}
