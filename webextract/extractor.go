// Package webextract 从任意 HTML 页面中抽取“正文内容 + 元信息”。
//
// 它用纯 Go 重新实现了 trafilatura / readability / newspaper / goose 这一类
// 工具的核心思路：
//
//  1. 一次 Readability 风格的 DOM 评分，定位并清洗出正文节点；
//  2. 一次元信息扫描，综合 <meta>、OpenGraph、Twitter Card 与 JSON-LD。
//
// 评分与清洗规则针对中文站点（含微信公众号文章）做了额外调优，例如按
// 中文标点统计“句子感”、按链接密度剔除导航/相关推荐等。
//
// 典型用法：
//
//	art, err := webextract.FromString(html, &webextract.Options{
//	        PageURL:       "https://example.com/post",
//	        FilterPhrases: true, // 顺手剔除署名/版权/“关注在看”等套话
//	})
package webextract

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html/charset"
)

// Article 是一次抽取的结构化结果。所有字段都带 json tag，可直接作为 API 返回值
// 序列化；调用方拿到后应视其为只读。
type Article struct {
	// === 元信息（字段对齐 go-trafilatura 的 Metadata）===
	URL          string    `json:"url,omitempty"`           // 源 URL（即 Options.PageURL）
	CanonicalURL string    `json:"canonical_url,omitempty"` // 规范链接（<link rel=canonical> 或 og:url）
	Hostname     string    `json:"hostname,omitempty"`      // 主机名/域名（从 URL 提取，去 www. 前缀）
	Title        string    `json:"title"`                   // 标题（已去除“ - 站点名”等尾缀）
	Author       string    `json:"author,omitempty"`        // 作者/来源
	PublishDate  string    `json:"publish_date,omitempty"`  // 发布时间（保留页面中的原始字符串）
	ParsedDate   time.Time `json:"-"`                       // 尽力解析后的时间（含 URL 路径日期兜底），失败为零值；JSON 见 publish_date
	Description  string    `json:"description,omitempty"`   // 摘要描述（多取自 meta description）
	SiteName     string    `json:"sitename,omitempty"`      // 站点名（og:site_name 等）
	Language     string    `json:"language,omitempty"`      // 语言（<html lang> 优先）
	PageType     string    `json:"page_type,omitempty"`     // 页面类型（og:type，如 article/website）
	License      string    `json:"license,omitempty"`       // 许可（rel=license 文本，或 CC 链接识别）
	TopImage     string    `json:"image,omitempty"`         // 首图/封面（og:image，缺失时回退正文首图）
	Images       []string  `json:"images,omitempty"`        // 正文内图片（绝对地址、已去重）
	Favicon      string    `json:"favicon,omitempty"`       // 站点图标
	Categories   []string  `json:"categories,omitempty"`    // 栏目/分类（article:section、面包屑）
	Tags         []string  `json:"tags,omitempty"`          // 标签（article:tag、rel=tag、news_keywords）
	Keywords     []string  `json:"keywords,omitempty"`      // 关键词（meta keywords，按逗号拆分）
	// Fingerprint 是正文文本的内容指纹（归一化后取哈希），可用于跨页去重。
	Fingerprint string `json:"fingerprint,omitempty"`

	// === 三种正文文本表示（按需取用）===

	// Text 是清洗后的【纯文本】正文，段落之间以 "\n\n" 分隔。
	Text string `json:"text"`
	// Markdown 是正文渲染成的【Markdown】文本（标题/段落/链接/图片/列表/引用/代码）。
	Markdown string `json:"markdown,omitempty"`
	// ContentHTMLNoMedia 是【仅含文本的正文 HTML】：已去除 <img>/<video>/<iframe>/<svg>
	// 等媒体，并拆掉 <a> 链接外壳（去标签与 href、保留链接文字），只留纯文字结构。
	// 无论 Options.StripMedia 是否开启都会填充——需要“只要文字、不要图片/链接”的
	// 纯净正文时用它（图片仍可从 Images 字段取得）。
	ContentHTMLNoMedia string `json:"content_html_no_media,omitempty"`

	// === 其它正文产物 ===

	// ContentHTML 是完整正文 HTML（保留图片，图片/链接已绝对化）。
	ContentHTML string `json:"content_html,omitempty"`
	// MarkdownNoMedia 是去除媒体后的正文 Markdown，与 ContentHTMLNoMedia 对应。
	MarkdownNoMedia string `json:"markdown_no_media,omitempty"`
	// Excerpt 是一段简短摘要：优先用 meta description，否则截取 Text 开头。
	Excerpt string `json:"excerpt,omitempty"`
	// WordCount 是粗略字数：CJK 按字计、其它按空白分词计（见 countWords）。
	WordCount int `json:"word_count"`
}

// Options 控制抽取行为。所有字段均可留空，normalizeOptions 会补默认值。
type Options struct {
	// PageURL 是源 URL，用于把正文里的相对链接/图片解析成绝对地址。
	PageURL string
	// Timeout 是 FromURL 抓取的超时时间，0 表示用默认值。
	Timeout time.Duration
	// UserAgent 是 FromURL 抓取时使用的 UA，空表示用默认值。
	UserAgent string
	// MinTextLength 是正文的最小字符数（按 rune 计）。抽取结果短于此值时，
	// 会被视为“疑似失败”，进而尝试退而求其次的策略。0 表示用默认值。
	MinTextLength int

	// FilterPhrases 开启“按句剔除套话”：把文本按元素边界 + 中文/ASCII 标点
	// 切成句子，满足以下任一条件的整句即从 Text / ContentHTML / Markdown 删除：
	//   - 包含内置短语表（或 ExtraPhrases）中的任意短语；
	//   - 以“1~4 个字的短标签 + 冒号”开头，如「来源：」「整理：」「编辑：」「图：」。
	// 用于清掉署名、来源、版权声明、以及微信式的“关注/在看/阅读原文”等文案。
	FilterPhrases bool
	// ExtraPhrases 是额外要剔除的短语。只要非空，FilterPhrases 即自动视为开启。
	ExtraPhrases []string

	// AggressiveFilter 启用「激进版」套话过滤：改用一份更狠的内置短语表
	// （aggressiveSkipPhrases，含 关注/分享/图/来源/编辑/记者/广告/http 等大量
	// 单字/双字通用词），适合对正文纯净度要求高、能容忍偶发漏字的场景（如喂给
	// 下游模型）。为 true 时自动视为开启短语过滤，无需再设 FilterPhrases。
	//
	// ⚠ 它比 FilterPhrases 误伤率高得多（子串匹配，"图" 命中 "地图"、"关注" 命中
	// "关注度"）。对纯净度不敏感的场景请用 FilterPhrases。ExtraPhrases 仍会叠加。
	AggressiveFilter bool

	// StripMedia 会从返回的 ContentHTML 与 Markdown 中删除
	// <img>/<video>/<audio>/<iframe>/<embed>/<svg> 等媒体元素；但 Images 切片
	// 仍会被填充（图片在删除前已采集）。
	StripMedia bool
}

// phraseFilterEnabled 报告本次是否需要执行“按句剔除套话”。
// 显式开启 FilterPhrases、开启 AggressiveFilter、或提供了 ExtraPhrases，都算开启。
func (o *Options) phraseFilterEnabled() bool {
	return o.FilterPhrases || o.AggressiveFilter || len(o.ExtraPhrases) > 0
}

const (
	defaultUserAgent = "Mozilla/5.0 (compatible; webextract/1.0; +https://example.com/bot)"
	defaultTimeout   = 20 * time.Second
	defaultMinText   = 25 // 正文最小字符数（rune）
)

// FromURL 抓取给定 URL 并抽取成 Article。
//
// 它会带上 UA 与 Accept 头发起 GET 请求，并校验 2xx 状态码；抓取与解析是
// 解耦的：真正的解析交给 FromReader 完成。若你已有 HTML（例如经过代理、
// 重试或无头浏览器渲染），优先用 FromString / FromReader。
func FromURL(rawURL string, opts *Options) (*Article, error) {
	o := normalizeOptions(opts)
	o.PageURL = rawURL // 以传入的 URL 作为相对地址解析的基准

	client := &http.Client{Timeout: o.Timeout}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", o.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch %s: status %d", rawURL, resp.StatusCode)
	}
	return FromReader(resp.Body, o)
}

// FromString 从 HTML 字符串抽取成 Article（推荐入口：抓取与解析解耦）。
func FromString(htmlStr string, opts *Options) (*Article, error) {
	return FromReader(strings.NewReader(htmlStr), normalizeOptions(opts))
}

// FromReader 从 HTML 流抽取成 Article。FromURL 与 FromString 最终都汇聚到这里。
func FromReader(r io.Reader, opts *Options) (*Article, error) {
	o := normalizeOptions(opts)
	// 字符集检测：很多中文站点是 GB2312/GBK（或 Big5/Shift-JIS）编码，若直接按
	// UTF-8 交给 goquery 解析会乱码。charset.NewReader 会读 BOM + <meta charset>
	// （并在缺失时自动嗅探），把非 UTF-8 内容转成 UTF-8 流。检测失败则用原始流兜底。
	if cr, err := charset.NewReader(r, ""); err == nil {
		r = cr
	}
	doc, err := goquery.NewDocumentFromReader(r)
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}
	return extract(doc, o)
}

// normalizeOptions 复制一份 opts（避免改动调用方对象）并补齐默认值。
func normalizeOptions(opts *Options) *Options {
	o := &Options{}
	if opts != nil {
		*o = *opts
	}
	if o.Timeout == 0 {
		o.Timeout = defaultTimeout
	}
	if o.UserAgent == "" {
		o.UserAgent = defaultUserAgent
	}
	if o.MinTextLength == 0 {
		o.MinTextLength = defaultMinText
	}
	return o
}

// extract 编排“元信息扫描 + 正文抽取”两趟处理，是真正的核心流程。
func extract(doc *goquery.Document, o *Options) (*Article, error) {
	// 解析基准 URL，用于把相对地址转成绝对地址；解析失败则保持 nil。
	var base *url.URL
	if o.PageURL != "" {
		if u, err := url.Parse(o.PageURL); err == nil {
			base = u
		}
	}

	art := &Article{URL: o.PageURL}

	// 1) 元信息扫描：必须在裁剪 <head> 之前，从 meta/og/twitter/json-ld 取信息。
	extractMetadata(doc, art, base)

	// 2) 正文抽取：在 doc 的克隆体上操作，不污染原始文档。
	node, _ := extractContent(doc, o)
	if node != nil {
		// 采集图片要在任何“删除媒体”之前进行，保证 Images 仍被填充。
		node.Find("img").Each(func(_ int, s *goquery.Selection) {
			if src := bestImageSrc(s); src != "" {
				art.Images = appendUnique(art.Images, absURL(src, base))
			}
		})

		// 可选的节点级后处理，顺序很关键：
		//   先删媒体 → 再按句剔除套话 → 最后清理因前两步而变空的容器。
		if o.StripMedia {
			stripMedia(node)
		}
		if o.phraseFilterEnabled() {
			filterPhrasesInNode(node, phrasesFor(o))
		}
		if o.StripMedia || o.phraseFilterEnabled() {
			removeEmpty(node)
		}

		// 渲染三种产物：绝对化后的 HTML、Markdown、纯文本。
		art.ContentHTML = absolutizeImages(htmlOf(node), base)
		art.Markdown = toMarkdown(art.ContentHTML)
		art.Text = nodeText(node)

		// 额外再产出一份“去媒体”版本：始终可用，方便只要文字的场景。
		// 若已开启 StripMedia，ContentHTML 本就无媒体，这里结果与之一致。
		if noMedia := stripMediaFromHTML(art.ContentHTML); noMedia != "" {
			art.ContentHTMLNoMedia = noMedia
			art.MarkdownNoMedia = toMarkdown(noMedia)
		}
	}

	// 标题回退链：og/twitter/json-ld 都没拿到时，依次回退 <title>、<h1>。
	if art.Title == "" {
		art.Title = strings.TrimSpace(doc.Find("title").First().Text())
	}
	if art.Title == "" {
		art.Title = strings.TrimSpace(doc.Find("h1").First().Text())
	}
	art.Title = cleanTitle(art.Title, art.SiteName)

	// 首图回退：元信息里没有封面时，用正文第一张图片。
	if art.TopImage == "" && len(art.Images) > 0 {
		art.TopImage = art.Images[0]
	}

	// 摘要 + 字数 + 内容指纹。
	if art.Description != "" {
		art.Excerpt = art.Description
	} else {
		art.Excerpt = truncateRunes(art.Text, 200)
	}
	art.WordCount = countWords(art.Text)
	art.Fingerprint = contentFingerprint(art.Text)

	return art, nil
}
