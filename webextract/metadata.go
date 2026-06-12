package webextract

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// extractMetadata 从 <head>（meta / OpenGraph / Twitter Card）与 JSON-LD 中
// 填充 art 的元信息字段，并在最后做若干兜底回退。
func extractMetadata(doc *goquery.Document, art *Article, base *url.URL) {
	metas := collectMeta(doc)

	// first 按优先级返回第一个非空的 meta 值。
	first := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := metas[k]; ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
		return ""
	}

	// 标题/描述/站点名：OpenGraph 优先，其次 Twitter，再次普通 meta。
	art.Title = first("og:title", "twitter:title", "title")
	art.Description = first("og:description", "twitter:description", "description")
	art.SiteName = first("og:site_name", "application-name")
	// 作者：覆盖常见的多种命名约定。
	art.Author = first("author", "article:author", "twitter:creator", "dc.creator", "parsely-author")
	// 发布时间：尽量多覆盖各家 CMS 的字段名。
	art.PublishDate = first(
		"article:published_time", "datepublished", "publishdate", "pubdate",
		"date", "dc.date", "dc.date.issued", "parsely-pub-date", "sailthru.date",
	)
	if img := first("og:image", "og:image:url", "twitter:image", "twitter:image:src"); img != "" {
		art.TopImage = absURL(img, base)
	}
	if kw := first("keywords"); kw != "" {
		for _, k := range strings.Split(kw, ",") {
			if k = strings.TrimSpace(k); k != "" {
				art.Keywords = appendUnique(art.Keywords, k)
			}
		}
	}
	// 页面类型（og:type，如 article / website）。
	art.PageType = first("og:type")

	// 分类/栏目：article:section（可多次）+ 面包屑导航。
	doc.Find(`meta[property="article:section"], meta[name="article:section"]`).Each(func(_ int, s *goquery.Selection) {
		if v := strings.TrimSpace(attr(s, "content")); v != "" {
			art.Categories = appendUnique(art.Categories, v)
		}
	})
	extractBreadcrumbs(doc, art)

	// 标签：article:tag（可多次）+ rel=tag 链接 + news_keywords。
	doc.Find(`meta[property="article:tag"], meta[name="article:tag"]`).Each(func(_ int, s *goquery.Selection) {
		if v := strings.TrimSpace(attr(s, "content")); v != "" {
			art.Tags = appendUnique(art.Tags, v)
		}
	})
	doc.Find(`a[rel~="tag"]`).Each(func(_ int, s *goquery.Selection) {
		if v := collapseSpaces(s.Text()); v != "" && lenRunes(v) <= 30 {
			art.Tags = appendUnique(art.Tags, v)
		}
	})
	if kw := first("news_keywords"); kw != "" {
		for _, k := range strings.Split(kw, ",") {
			if k = strings.TrimSpace(k); k != "" {
				art.Tags = appendUnique(art.Tags, k)
			}
		}
	}

	// 许可：rel=license 的文本，或从 Creative Commons 链接识别。
	extractLicense(doc, art)

	// 语言：优先 <html lang>，否则退回 meta。
	if lang, ok := doc.Find("html").Attr("lang"); ok && strings.TrimSpace(lang) != "" {
		art.Language = strings.TrimSpace(lang)
	} else {
		art.Language = first("og:locale", "language", "dc.language")
	}

	// 规范链接：<link rel=canonical> 优先，否则用 og:url。
	if href, ok := doc.Find(`link[rel="canonical"]`).Attr("href"); ok {
		art.CanonicalURL = absURL(href, base)
	} else if v := first("og:url"); v != "" {
		art.CanonicalURL = absURL(v, base)
	}

	// 站点图标：取第一个 rel 含 icon 的 link。
	doc.Find(`link[rel~="icon"]`).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if href, ok := s.Attr("href"); ok && strings.TrimSpace(href) != "" {
			art.Favicon = absURL(href, base)
			return false // 找到即停
		}
		return true
	})

	// JSON-LD 通常能更可靠地补充/覆盖作者与日期（仅填补当前为空的字段）。
	applyJSONLD(doc, art, base)

	// 作者兜底：rel=author / 常见 byline 类名。若结果像一整句话（词数过多），
	// 多半不是人名，丢弃。
	if art.Author == "" {
		art.Author = strings.TrimSpace(doc.Find(`[rel="author"], .author, .byline, .post-author`).First().Text())
		art.Author = collapseSpaces(art.Author)
		if len(strings.Fields(art.Author)) > 8 {
			art.Author = ""
		}
	}

	// 日期兜底：<time datetime>。
	if art.PublishDate == "" {
		if dt, ok := doc.Find("time[datetime]").First().Attr("datetime"); ok {
			art.PublishDate = strings.TrimSpace(dt)
		}
	}
	art.ParsedDate = parseDate(art.PublishDate)
	// 日期再兜底：从 URL 路径里挖 /2026/05/30/ 这类日期（trafilatura 同款思路）。
	if art.ParsedDate.IsZero() {
		if t := dateFromURL(firstNonEmpty(art.CanonicalURL, art.URL)); !t.IsZero() {
			art.ParsedDate = t
			if art.PublishDate == "" {
				art.PublishDate = t.Format("2006-01-02")
			}
		}
	}

	// 主机名/域名：优先规范链接，其次源 URL。
	art.Hostname = hostnameOf(firstNonEmpty(art.CanonicalURL, art.URL), base)
}

// extractBreadcrumbs 从面包屑导航里取栏目层级，填入 Categories。
// 兼容 schema.org BreadcrumbList（microdata）与常见的 .breadcrumb / nav 标记，
// 并剔除“首页/home”这类无意义层级与末尾的当前页标题。
func extractBreadcrumbs(doc *goquery.Document, art *Article) {
	sel := doc.Find(`[itemtype$="BreadcrumbList"] [itemprop="name"], .breadcrumb a, .breadcrumbs a, nav[aria-label="breadcrumb"] a, .crumb a`)
	var items []string
	sel.Each(func(_ int, s *goquery.Selection) {
		t := collapseSpaces(s.Text())
		if t == "" || lenRunes(t) > 20 {
			return
		}
		switch strings.ToLower(t) {
		case "首页", "主页", "home", "首頁":
			return
		}
		items = append(items, t)
	})
	// 末项通常是当前文章标题，不算栏目，去掉。
	if len(items) > 1 {
		items = items[:len(items)-1]
	}
	for _, it := range items {
		art.Categories = appendUnique(art.Categories, it)
	}
}

// extractLicense 提取内容许可：优先 rel=license 链接/元素的可见文本，
// 否则从其 href 识别 Creative Commons 许可名（如 "CC BY-SA 4.0"）。
func extractLicense(doc *goquery.Document, art *Article) {
	doc.Find(`a[rel~="license"], link[rel~="license"]`).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if t := collapseSpaces(s.Text()); t != "" && lenRunes(t) <= 60 {
			art.License = t
			return false
		}
		if name := ccLicenseName(attr(s, "href")); name != "" {
			art.License = name
			return false
		}
		return true
	})
}

// collectMeta 收集所有 <meta>，以其小写后的 name/property/itemprop/http-equiv
// 为键存入 map（同名取首次出现的值）。
func collectMeta(doc *goquery.Document) map[string]string {
	m := make(map[string]string)
	doc.Find("meta").Each(func(_ int, s *goquery.Selection) {
		content, ok := s.Attr("content")
		if !ok || strings.TrimSpace(content) == "" {
			return
		}
		for _, attr := range []string{"property", "name", "itemprop", "http-equiv"} {
			if key, ok := s.Attr(attr); ok {
				key = strings.ToLower(strings.TrimSpace(key))
				if _, exists := m[key]; !exists { // 首次出现者胜出
					m[key] = content
				}
			}
		}
	})
	return m
}

// --- JSON-LD ---

// applyJSONLD 解析页面里所有 <script type="application/ld+json"> 并逐个对象
// 回填到 art。
func applyJSONLD(doc *goquery.Document, art *Article, base *url.URL) {
	doc.Find(`script[type="application/ld+json"]`).Each(func(_ int, s *goquery.Selection) {
		raw := strings.TrimSpace(s.Text())
		if raw == "" {
			return
		}
		for _, obj := range flattenJSONLD(raw) {
			applyJSONLDObject(obj, art, base)
		}
	})
}

// flattenJSONLD 解析一段 JSON-LD，并把其中所有候选对象摊平返回：既展开顶层
// 数组，也展开 @graph 容器。
func flattenJSONLD(raw string) []map[string]any {
	var out []map[string]any

	// 情形一：顶层是单个对象（可能带 @graph）。
	var single map[string]any
	if err := json.Unmarshal([]byte(raw), &single); err == nil {
		if g, ok := single["@graph"].([]any); ok {
			for _, item := range g {
				if obj, ok := item.(map[string]any); ok {
					out = append(out, obj)
				}
			}
		}
		out = append(out, single)
		return out
	}

	// 情形二：顶层是对象数组。
	var arr []map[string]any
	if err := json.Unmarshal([]byte(raw), &arr); err == nil {
		out = append(out, arr...)
	}
	return out
}

// jsonLDArticleTypes 是被视为“文章类”的 JSON-LD @type 集合（小写）。
var jsonLDArticleTypes = map[string]bool{
	"article": true, "newsarticle": true, "blogposting": true,
	"reportagenewsarticle": true, "techarticle": true, "webpage": true,
}

// applyJSONLDObject 从单个 JSON-LD 对象里补齐 art 中仍为空的字段。
// 非文章类对象里，只有 Person / Organization 仍可能提供作者信息，其余跳过。
func applyJSONLDObject(obj map[string]any, art *Article, base *url.URL) {
	typ := strings.ToLower(jsonString(obj["@type"]))
	if typ != "" && !jsonLDArticleTypes[typ] {
		if typ != "person" && typ != "organization" {
			return
		}
	}

	if art.Title == "" {
		art.Title = jsonString(obj["headline"])
	}
	if art.Description == "" {
		art.Description = jsonString(obj["description"])
	}
	if art.PublishDate == "" {
		art.PublishDate = firstNonEmpty(jsonString(obj["datePublished"]), jsonString(obj["dateCreated"]))
	}
	if art.Author == "" {
		art.Author = jsonLDAuthor(obj["author"])
	}
	if art.TopImage == "" {
		if img := jsonLDImage(obj["image"]); img != "" {
			art.TopImage = absURL(img, base)
		}
	}
}

// jsonLDAuthor 从 author 字段提取作者名，兼容三种形态：
// 字符串、{name: ...} 对象、以及二者混合的数组（多作者以 ", " 连接）。
func jsonLDAuthor(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		return jsonString(t["name"])
	case []any:
		var names []string
		for _, item := range t {
			if m, ok := item.(map[string]any); ok {
				if n := jsonString(m["name"]); n != "" {
					names = append(names, n)
				}
			} else if s, ok := item.(string); ok {
				names = append(names, s)
			}
		}
		return strings.Join(names, ", ")
	}
	return ""
}

// jsonLDImage 从 image 字段提取图片地址，兼容字符串、{url: ...} 对象与数组
// （数组取第一个，递归处理）。
func jsonLDImage(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		return jsonString(t["url"])
	case []any:
		if len(t) > 0 {
			return jsonLDImage(t[0])
		}
	}
	return ""
}

// jsonString 把 any 安全地取成去空白的字符串；非字符串返回空串。
func jsonString(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// --- 日期解析 ---

// dateLayouts 是依次尝试的时间格式：RFC3339 / ISO8601 系列在前（最常命中），
// 兼顾中式 "2006年01月02日" 与若干英文格式。
var dateLayouts = []string{
	time.RFC3339, time.RFC3339Nano, "2006-01-02T15:04:05Z07:00",
	"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04",
	"2006-01-02", "2006/01/02 15:04:05", "2006/01/02", "2006年01月02日",
	"02 Jan 2006", "Jan 2, 2006", "January 2, 2006", time.RFC1123Z, time.RFC1123,
}

// parseDate 尽力把日期字符串解析成 time.Time；全部格式都不匹配时返回零值。
func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
