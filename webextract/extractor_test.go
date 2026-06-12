package webextract_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/yulin-dust/commonlib/webextract"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// 非 UTF-8 页面（GB2312/GBK）应被正确识别字符集并解析，不出现乱码。
func TestGBKPageDecoded(t *testing.T) {
	const page = `<!DOCTYPE html><html><head><meta charset="gb2312"><title>高考新闻_中新网</title></head>
<body><article>
<h1>高考最热词男生抱昏迷女生</h1>
<p>这是一段足够长的中文正文，用来作为主体内容被抽取出来，确保结果非空且可断言，里面有准考证、考场等词。</p>
<p>第二段继续补充内容，增加长度、句子与标点，帮助可读性评分稳稳锁定到这个正文容器上面来。</p>
</article></body></html>`

	gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(page))
	if err != nil {
		t.Fatal(err)
	}
	art, err := webextract.FromReader(bytes.NewReader(gbk), &webextract.Options{PageURL: "https://news.example.cn/x.shtml"})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !strings.Contains(art.Title, "高考新闻") {
		t.Errorf("标题乱码或缺失: %q", art.Title)
	}
	for _, frag := range []string{"高考最热词", "准考证", "考场"} {
		if !strings.Contains(art.Text, frag) {
			t.Errorf("正文乱码或缺失 %q\n%s", frag, art.Text)
		}
	}
}

const sampleHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <title>量子计算取得新突破 - 科技日报</title>
  <meta property="og:title" content="量子计算取得新突破">
  <meta property="og:site_name" content="科技日报">
  <meta property="og:description" content="研究团队实现了关键性的纠错里程碑。">
  <meta property="og:image" content="/img/quantum.jpg">
  <meta name="author" content="张三">
  <meta property="article:published_time" content="2026-05-30T09:15:00+08:00">
  <link rel="canonical" href="https://news.example.com/quantum">
  <script type="application/ld+json">
  {"@type":"NewsArticle","headline":"量子计算取得新突破","author":{"@type":"Person","name":"张三"},"datePublished":"2026-05-30T09:15:00+08:00"}
  </script>
</head>
<body>
  <header class="site-header"><nav><a href="/">首页</a><a href="/tech">科技</a></nav></header>
  <div class="ad-banner"><a href="/promo">广告：点击赢大奖</a></div>
  <article class="post-content">
    <h1>量子计算取得新突破</h1>
    <p>研究团队今天宣布，他们在量子纠错领域取得了关键性进展。这一成果有望大幅提升量子计算机的稳定性，是迈向实用化的重要一步。</p>
    <p>据介绍，新方法将逻辑量子比特的错误率降低了一个数量级。专家认为，这意味着大规模量子计算的可行性显著增强，未来在材料科学、药物研发等领域将产生深远影响。</p>
    <figure><img src="/img/quantum.jpg" alt="量子芯片"></figure>
    <p>不过，研究人员也强调，距离真正的商用仍有许多工程挑战需要克服，包括制冷、控制系统的规模化等问题。</p>
  </article>
  <aside class="related"><h3>相关阅读</h3><ul><li><a href="/a">文章一</a></li><li><a href="/b">文章二</a></li></ul></aside>
  <div class="comments"><h3>评论</h3><p>这篇评论很长很长，长到看起来像正文一样，但其实是用户评论，不应该被当作正文提取出来才对。</p></div>
  <footer>版权所有 © 科技日报</footer>
</body>
</html>`

func TestFromString(t *testing.T) {
	// 或从已有 HTML 解析（推荐：抓取与解析解耦，便于配合代理/重试/JS 渲染）
	art, err := webextract.FromString(sampleHTML, &webextract.Options{
		PageURL: "https://news.example.com/article", // 用于把相对链接/图片转成绝对地址
	})
	if err != nil {
		t.Log(err)
		return
	}

	if art == nil {
		t.Log("air is nil")
		return
	}

	fmt.Println(art.Title, art.Author, art.PublishDate)
	fmt.Println(art.Text)        // 纯文本正文
	fmt.Println(art.ContentHTML) // 清洗后的正文 HTML
}

func TestFromURL(t *testing.T) {
	art, err := webextract.FromURL("https://www.chinanews.com.cn/edu/2013/06-08/4911353.shtml", &webextract.Options{
		UserAgent:     "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36",
		FilterPhrases: true,
	})
	if err != nil {
		t.Log(err)
		return
	}
	if art == nil {
		t.Log("art is nil")
		return
	}

	dumpArticle(t, art)
}

// dumpArticle 打印一个 Article 的【所有】字段：先逐项列出元信息，再依次给出
// 三种正文文本表示与其它正文产物，最后附一份完整 JSON。
func dumpArticle(t *testing.T, art *webextract.Article) {
	t.Helper()

	p := func(label, val string) {
		if strings.TrimSpace(val) != "" {
			t.Logf("%-14s %s", label+":", val)
		}
	}
	ps := func(label string, vals []string) {
		if len(vals) > 0 {
			t.Logf("%-14s %s", label+":", strings.Join(vals, " | "))
		}
	}

	t.Log("==================== 元信息 ====================")
	p("URL", art.URL)
	p("CanonicalURL", art.CanonicalURL)
	p("Hostname", art.Hostname)
	p("Title", art.Title)
	p("Author", art.Author)
	p("PublishDate", art.PublishDate)
	if !art.ParsedDate.IsZero() {
		p("ParsedDate", art.ParsedDate.Format("2006-01-02 15:04:05"))
	}
	p("Description", art.Description)
	p("Excerpt", art.Excerpt)
	p("SiteName", art.SiteName)
	p("Language", art.Language)
	p("PageType", art.PageType)
	p("License", art.License)
	p("TopImage", art.TopImage)
	ps("Images", art.Images)
	p("Favicon", art.Favicon)
	ps("Categories", art.Categories)
	ps("Tags", art.Tags)
	ps("Keywords", art.Keywords)
	p("Fingerprint", art.Fingerprint)
	t.Logf("%-14s %d", "WordCount:", art.WordCount)

	t.Log("==================== 纯文本 Text ====================")
	t.Logf("\n%s", art.Text)
	t.Log("==================== Markdown ====================")
	t.Logf("\n%s", art.Markdown)
	t.Log("============ 仅含文本的 HTML（ContentHTMLNoMedia）============")
	t.Logf("\n%s", art.ContentHTMLNoMedia)
	t.Log("==================== 完整 ContentHTML（含媒体）====================")
	t.Logf("\n%s", art.ContentHTML)
	t.Log("==================== MarkdownNoMedia ====================")
	t.Logf("\n%s", art.MarkdownNoMedia)

	t.Log("==================== 完整 JSON ====================")
	b, err := json.MarshalIndent(art, "", "  ")
	if err != nil {
		t.Logf("json marshal: %v", err)
		return
	}
	t.Logf("\n%s", b)
}

func TestExtractContent(t *testing.T) {
	art, err := webextract.FromString(sampleHTML, &webextract.Options{PageURL: "https://news.example.com/quantum"})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	// Body paragraphs must be present.
	for _, frag := range []string{"量子纠错领域", "降低了一个数量级", "工程挑战"} {
		if !strings.Contains(art.Text, frag) {
			t.Errorf("content missing %q\n--- got ---\n%s", frag, art.Text)
		}
	}
	// Boilerplate must be gone.
	for _, frag := range []string{"广告", "相关阅读", "用户评论", "版权所有", "首页"} {
		if strings.Contains(art.Text, frag) {
			t.Errorf("content leaked boilerplate %q\n--- got ---\n%s", frag, art.Text)
		}
	}
	if art.WordCount < 50 {
		t.Errorf("word count too low: %d", art.WordCount)
	}
	t.Logf("title=%q author=%q date=%q words=%d", art.Title, art.Author, art.PublishDate, art.WordCount)
	t.Logf("text:\n%s", art.Text)
	t.Logf("Markdown:\n%s", art.Markdown)
}

// 三种正文文本表示都应产出，且“仅含文本的 HTML”确实不含媒体标签。
func TestTextRepresentations(t *testing.T) {
	art, err := webextract.FromString(sampleHTML, &webextract.Options{PageURL: "https://news.example.com/quantum"})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if strings.TrimSpace(art.Text) == "" {
		t.Error("纯文本 Text 为空")
	}
	if strings.TrimSpace(art.Markdown) == "" {
		t.Error("Markdown 为空")
	}
	if strings.TrimSpace(art.ContentHTMLNoMedia) == "" {
		t.Error("仅含文本的 HTML 为空")
	}
	for _, tag := range []string{"<img", "<video", "<iframe", "<svg", "<a ", "<a>"} {
		if strings.Contains(strings.ToLower(art.ContentHTMLNoMedia), tag) {
			t.Errorf("ContentHTMLNoMedia 不应含媒体/链接标签 %q", tag)
		}
	}
	// 链接被拆壳但文字应保留（量子样本里没有链接，这里只确保不报错；
	// 真正的链接文字保留在 TestUnwrapKeepsLinkText 验证）。
	// 完整 HTML 仍应保留图片。
	if !strings.Contains(strings.ToLower(art.ContentHTML), "<img") {
		t.Error("ContentHTML 应保留 <img>")
	}
}

// 仅含文本的 HTML：<a> 链接应被拆壳（去标签/href），但链接文字应保留。
func TestUnwrapKeepsLinkText(t *testing.T) {
	const html = `<!DOCTYPE html><html><body><article>
<h1>标题标题标题</h1>
<p>这是一段足够长的正文，里面有一个<a href="https://x.example/topic">指向专题</a>的链接，
还有更多文字让打分器把这个容器判为正文，从而产出非空的正文文本供后续断言使用。</p>
<p>第二段继续补充句子、标点和长度，帮助可读性评分稳稳锁定到这个 article 元素上面来。</p>
</article></body></html>`

	art, err := webextract.FromString(html, &webextract.Options{PageURL: "https://x.example/p"})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	low := strings.ToLower(art.ContentHTMLNoMedia)
	// 注意用 "</a>" 而非 "<a"——后者会误命中 <article>/<aside>。
	if strings.Contains(low, "</a>") || strings.Contains(low, "<a ") || strings.Contains(low, "<a>") {
		t.Errorf("ContentHTMLNoMedia 仍含 <a>:\n%s", art.ContentHTMLNoMedia)
	}
	if strings.Contains(low, "href") {
		t.Errorf("ContentHTMLNoMedia 仍含 href:\n%s", art.ContentHTMLNoMedia)
	}
	if !strings.Contains(art.ContentHTMLNoMedia, "指向专题") {
		t.Errorf("链接文字“指向专题”应被保留:\n%s", art.ContentHTMLNoMedia)
	}
	// 对照：完整 ContentHTML 仍应保留 <a href>。
	if !strings.Contains(strings.ToLower(art.ContentHTML), "<a") {
		t.Error("完整 ContentHTML 应保留 <a> 链接")
	}
}

// AggressiveFilter 为 true 时，应剔除默认过滤放过的激进套话（如“关注公众号”
// 这类引导句、含 http 的行、“记者/编辑”署名等），而正文主体保留。
func TestAggressiveFilter(t *testing.T) {
	const html = `<!DOCTYPE html><html><body><article>
<h1>正文标题正文标题</h1>
<p>第一段是真正的正文内容，讲述了一件事情的来龙去脉，足够长以便被识别为正文主体，并保留下来。</p>
<p>关注公众号「某某」获取更多资讯，扫码加入读者群。</p>
<p>第二段继续叙述事件的后续发展，提供了更多细节与背景，确保正文足够充实可被稳定抽取。</p>
<p>更多详情请访问 http://example.com/promo 查看。</p>
<p>记者：张三　编辑：李四</p>
</article></body></html>`

	opts := func(aggr bool) *webextract.Options {
		return &webextract.Options{PageURL: "https://x.example/p", AggressiveFilter: aggr}
	}

	// 默认（不激进）：放过这些引导/署名句。
	base, err := webextract.FromString(html, opts(false))
	if err != nil {
		t.Fatal(err)
	}
	// 激进：应剔除它们。
	aggr, err := webextract.FromString(html, opts(true))
	if err != nil {
		t.Fatal(err)
	}

	// 正文主体两版都应保留。
	for _, frag := range []string{"来龙去脉", "后续发展"} {
		if !strings.Contains(aggr.Text, frag) {
			t.Errorf("激进过滤误删正文 %q\n%s", frag, aggr.Text)
		}
	}
	// 激进版应剔除这些套话。
	for _, frag := range []string{"关注公众号", "扫码加入读者群", "http://example.com", "记者：张三"} {
		if strings.Contains(aggr.Text, frag) {
			t.Errorf("激进过滤未剔除套话 %q\n%s", frag, aggr.Text)
		}
	}
	t.Logf("默认版长度=%d 激进版长度=%d", len([]rune(base.Text)), len([]rune(aggr.Text)))
	if len([]rune(aggr.Text)) >= len([]rune(base.Text)) {
		t.Errorf("激进版应更短：base=%d aggr=%d", len([]rune(base.Text)), len([]rune(aggr.Text)))
	}
}

// 面包屑应被剔除：既覆盖「自创 class 名」（bread-page，走 reBoiler）也覆盖
// 「无 class 的 <p> 形态面包屑」（走结构启发式 isBreadcrumbish）；同时正文里带
// 链接的正常段落不应被误删。
func TestBreadcrumbStripped(t *testing.T) {
	const html = `<!DOCTYPE html><html><body>
<div class="main-wrap">
  <p class="bread-page"><a href="/">首页CRUMB</a> &gt; <a href="/c">栏目CRUMB</a> &gt; <a href="/x">游戏新闻CRUMB</a> &gt; 当前标题</p>
  <article>
    <p><a href="/a">导航BARE</a> › <a href="/b">面板BARE</a> › <a href="/c">资讯BARE</a></p>
    <h1>真正的文章标题在这里出现</h1>
    <p>这是正文第一段，内容足够长以便被识别为正文主体，讲述了一件事情的来龙去脉与若干细节，确保抽取稳定可靠。</p>
    <p>这是正文第二段，继续补充更多句子、标点与长度，帮助打分器牢牢锁定到正文容器上面来，不至于跑偏。</p>
    <p>正文里有一个<a href="/topic">站内链接</a>，这一段是正常段落，足够长、链接占比低，不应被当成面包屑误删掉。</p>
  </article>
</div>
</body></html>`

	art, err := webextract.FromString(html, &webextract.Options{PageURL: "https://e.example/p"})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	// 两类面包屑的链接标签都不应出现在正文里。
	for _, frag := range []string{"首页CRUMB", "栏目CRUMB", "游戏新闻CRUMB", "导航BARE", "面板BARE", "资讯BARE"} {
		if strings.Contains(art.Text, frag) {
			t.Errorf("面包屑泄漏 %q\n--- text ---\n%s", frag, art.Text)
		}
	}
	// 正文主体与正文内的正常链接段落应保留。
	for _, frag := range []string{"来龙去脉", "正文第二段", "站内链接"} {
		if !strings.Contains(art.Text, frag) {
			t.Errorf("正文缺失 %q\n--- text ---\n%s", frag, art.Text)
		}
	}
}

// trafilatura 风格的元信息字段应被填充。
func TestTrafilaturaStyleMetadata(t *testing.T) {
	const html = `<!DOCTYPE html><html lang="en">
<head>
  <title>Big Story</title>
  <meta property="og:title" content="Big Story">
  <meta property="og:type" content="article">
  <meta property="og:site_name" content="Example News">
  <meta property="article:section" content="Technology">
  <meta property="article:tag" content="ai">
  <meta property="article:tag" content="research">
  <link rel="license" href="https://creativecommons.org/licenses/by-sa/4.0/">
  <nav aria-label="breadcrumb"><a href="/">Home</a><a href="/tech">Tech</a><a href="/tech/ai">AI</a><a href="/tech/ai/x">Big Story</a></nav>
</head>
<body>
  <article><h1>Big Story</h1>
  <p>This is a sufficiently long article body paragraph so that the extractor treats this container as the main content and produces non-empty text output for downstream checks.</p>
  <p>A second paragraph adds more sentences, more punctuation, and more length, helping the readability scorer lock onto this article element confidently.</p>
  </article>
</body></html>`

	art, err := webextract.FromString(html, &webextract.Options{PageURL: "https://example.com/2026/05/30/big-story.html"})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if art.Hostname != "example.com" {
		t.Errorf("hostname=%q，期望 example.com", art.Hostname)
	}
	if art.PageType != "article" {
		t.Errorf("page_type=%q，期望 article", art.PageType)
	}
	if art.License != "CC BY-SA 4.0" {
		t.Errorf("license=%q，期望 CC BY-SA 4.0", art.License)
	}
	if !contains(art.Tags, "ai") || !contains(art.Tags, "research") {
		t.Errorf("tags=%v，期望含 ai/research", art.Tags)
	}
	if !contains(art.Categories, "Technology") && !contains(art.Categories, "Tech") {
		t.Errorf("categories=%v，期望含栏目", art.Categories)
	}
	if contains(art.Categories, "Home") {
		t.Errorf("面包屑不应保留 Home: %v", art.Categories)
	}
	// 日期从 URL 路径兜底解析。
	if art.ParsedDate.IsZero() || art.ParsedDate.Format("2006-01-02") != "2026-05-30" {
		t.Errorf("date 未从 URL 解析到 2026-05-30，实际 %v", art.ParsedDate)
	}
	if art.Fingerprint == "" {
		t.Error("fingerprint 应非空")
	}
	t.Logf("hostname=%s type=%s license=%s tags=%v cats=%v date=%s fp=%s",
		art.Hostname, art.PageType, art.License, art.Tags, art.Categories,
		art.ParsedDate.Format("2006-01-02"), art.Fingerprint)
}

func contains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}
