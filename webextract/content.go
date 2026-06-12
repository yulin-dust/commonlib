package webextract

import (
	"bytes"
	"math"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

// 下面这组正则用于根据元素的 class / id / role 判断它“像不像正文”。
// 全部用 (?i) 忽略大小写，匹配的是常见的英文命名约定（CMS、模板、广告位等）。
var (
	// reUnlikely：几乎从不是正文的区域（广告、面包屑、评论、页眉页脚、侧栏、
	// 社交分享、分页、弹窗、订阅、cookie 提示等）。
	reUnlikely = regexp.MustCompile(`(?i)-ad-|ai2html|banner|breadcrumb|combx|comment|community|cover-wrap|disqus|extra|footer|gdpr|header|legends|menu|related|remark|replies|rss|shoutbox|sidebar|skyscraper|social|sponsor|supplemental|ad-break|agegate|pagination|pager|popup|yom-remote|nav|share|promo|subscribe|newsletter|cookie|modal`)
	// reMaybe：即便命中了 reUnlikely，只要同时命中这里（看起来含正文语义），
	// 也予以保留，避免误删 class="article-comment-... " 这类正文容器。
	reMaybe = regexp.MustCompile(`(?i)and|article|body|column|content|main|shadow|post|entry|text|story`)
	// rePositive / reNegative：评分时使用的强正/强负信号。
	rePositive = regexp.MustCompile(`(?i)article|body|content|entry|hentry|h-entry|main|page|pagination|post|text|blog|story`)
	reNegative = regexp.MustCompile(`(?i)-ad-|hidden|^hid$| hid$|^hid |banner|combx|comment|com-|contact|foot|footer|footnote|gdpr|masthead|media|meta|outbrain|promo|related|scroll|share|shoutbox|sidebar|skyscraper|sponsor|shopping|tags|widget`)
	// reBoiler：强样板区标记。命中的元素在预处理阶段会被无条件删除（不看 reMaybe）——
	// 这些都是近乎通用的非正文区域：评论、侧栏、相关/推荐列表、标签云、友链/链接墙、
	// 分享栏、页脚等。前缀的词边界 ((^|[-_ ])) 用来避免误伤 "content"、"main" 等词内子串。
	// 注意：用 "bread"（而非仅 "breadcrumb"）覆盖 bread-page / bread-nav 等自创命名的面包屑。
	reBoiler = regexp.MustCompile(`(?i)(^|[-_ ])(comment|disqus|reply|share|social|sidebar|side-bar|related|relate|recommend|recom|similar|guess|aggregat|other-?game|hot-?game|hot-?news|hot-?video|news-?list|rank-?list|tag-?(list|tit|cloud|nav)|bread|crumb|page-?break|pager|pagination|popup|modal|cookie|newsletter|subscribe|promo|advert|sponsor|footer|copyright|nbot|friend-?link|link-?list|right-recom|right-con)`)
	// reHidden：内联 style 中把元素隐藏起来的写法（display:none / visibility:hidden）。
	reHidden = regexp.MustCompile(`(?i)(^|;)\s*(display\s*:\s*none|visibility\s*:\s*hidden)\s*(;|$)`)
)

// stripTags 是预处理阶段直接整体删除的标签：脚本/样式/表单/嵌入媒体以及
// 导航、页脚、侧边栏等结构性非正文标签。
var stripTags = []string{
	"script", "style", "noscript", "iframe", "svg", "form", "button",
	"input", "select", "textarea", "object", "embed", "link", "meta",
	"nav", "footer", "aside",
}

// scored 保存一个候选节点及其内容得分。
type scored struct {
	sel   *goquery.Selection
	score float64
}

// extractContent 返回“最佳正文节点”（一个游离、已清洗的克隆）及其 HTML。
// 它绝不会返回未经清洗的原始标记。
//
// 整体流程：克隆文档 → 预处理去噪 → 给候选容器评分 → 选出最高分 →
// 合并相邻正文块 → 清洗 → 若结果过薄则退回到“单个最佳节点”再清洗。
func extractContent(doc *goquery.Document, o *Options) (*goquery.Selection, string) {
	clone := goquery.CloneDocument(doc) // 在克隆体上随意改动，不影响元信息扫描所用的原文档
	preprocess(clone)

	candidates := scoreCandidates(clone)
	top := pickTop(candidates)

	var article *goquery.Selection
	if top != nil {
		article = assembleArticle(top, candidates)
	}
	// 评分没选出东西时，退回到整个 <body>（仍会清洗）。
	if article == nil || article.Length() == 0 {
		article = wrapNodes(selNodes(clone.Find("body")))
	}
	if article == nil || article.Length() == 0 {
		return nil, ""
	}

	cleanArticle(article)

	// 若“合并 + 清洗”后的结果太薄（短于 MinTextLength），改用单个最高分节点
	// 单独清洗——它仍是清洗过的，绝不会回退到未清洗的 body。取两者中更长的。
	if top != nil && lenRunes(strings.TrimSpace(article.Text())) < o.MinTextLength {
		if alt := wrapNodes([]*html.Node{top.sel.Nodes[0]}); alt != nil && alt.Length() > 0 {
			cleanArticle(alt)
			if lenRunes(strings.TrimSpace(alt.Text())) > lenRunes(strings.TrimSpace(article.Text())) {
				article = alt
			}
		}
	}

	return article, htmlOf(article)
}

// preprocess 删除垃圾标签，以及一望可知不是正文的容器。
func preprocess(doc *goquery.Document) {
	// 1) 整体删除脚本/样式/媒体/结构性标签。
	for _, tag := range stripTags {
		doc.Find(tag).Remove()
	}
	// 2) 删除残留在树里的 HTML 注释节点。
	doc.Find("*").Contents().Each(func(_ int, s *goquery.Selection) {
		if len(s.Nodes) > 0 && s.Nodes[0].Type == html.CommentNode {
			s.Remove()
		}
	})
	// 3) 删除被隐藏的元素：内联 display:none / visibility:hidden，或带 hidden 属性。
	//    这些不会承载可见正文（但 body/html 永不删）。
	doc.Find("[style]").Each(func(_ int, s *goquery.Selection) {
		if reHidden.MatchString(attr(s, "style")) {
			if n := nodeName(s); n != "body" && n != "html" {
				s.Remove()
			}
		}
	})
	doc.Find("[hidden]").Each(func(_ int, s *goquery.Selection) {
		if n := nodeName(s); n != "body" && n != "html" {
			s.Remove()
		}
	})
	// 4) 按 class/id/role 删除“不像正文”的候选容器。
	doc.Find("*").Each(func(_ int, s *goquery.Selection) {
		matchStr := strings.ToLower(attr(s, "class") + " " + attr(s, "id") + " " + attr(s, "role"))
		if strings.TrimSpace(matchStr) == "" {
			return
		}
		node := nodeName(s)
		// body/html/article 是天然的正文宿主，永不在此删除。
		if node == "body" || node == "html" || node == "article" {
			return
		}
		// 强样板区：无条件删除（不看 reMaybe）。
		if reBoiler.MatchString(matchStr) {
			s.Remove()
			return
		}
		// 一般不像正文：命中 reUnlikely 且未命中 reMaybe 时才删。
		if reUnlikely.MatchString(matchStr) && !reMaybe.MatchString(matchStr) {
			s.Remove()
		}
	})
}

// scoreCandidates 给“段落的各级祖先容器”累加内容得分，返回 节点 -> 得分 的映射。
//
// 评分直觉（沿用 Readability）：真正的正文往往是“若干带标点的较长段落”聚在
// 同一个容器里。于是每个像样的段落都会把分数加到它的父、祖父容器上；含正文
// 越多的容器分数越高。最后再按链接密度打折，惩罚“满是链接”的导航/聚合页。
func scoreCandidates(doc *goquery.Document) map[*html.Node]*scored {
	candidates := make(map[*html.Node]*scored)

	// get 取/建某节点的候选记录，初始分 = class/id 信号分 + 标签基础分。
	get := func(s *goquery.Selection) *scored {
		n := s.Nodes[0]
		if c, ok := candidates[n]; ok {
			return c
		}
		c := &scored{sel: s, score: classScore(s) + tagScore(s)}
		candidates[n] = c
		return c
	}

	doc.Find("p, td, pre, article, section, div").Each(func(_ int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		runeLen := lenRunes(text)
		if runeLen < 25 { // 太短的块不计入，避免噪声
			return
		}

		// 该块本身的基础分：基础 1 分 + 标点数（逗号/中文标点，越多越像散文）
		// + 长度奖励（每 100 字 +1，最多 +3）。
		contentScore := 1.0
		contentScore += float64(punctCount(text))
		contentScore += math.Min(math.Floor(float64(runeLen)/100.0), 3.0)

		// 把分数加到父容器（全额）与祖父容器（半额）。
		parent := s.Parent()
		if parent.Length() > 0 && parent.Nodes[0] != nil {
			get(parent).score += contentScore
			grand := parent.Parent()
			if grand.Length() > 0 && grand.Nodes[0] != nil {
				get(grand).score += contentScore / 2.0
			}
		}

		// 给节点“自有的直接文本”加分（仅直接文本子节点，不含嵌套元素里的文本）。
		// 这奖励了那些把裸文本直接塞在容器里的“真正的内容容器”——这在很多
		// CMS/SEO 页面上很常见（正文没有用 <p> 包裹），让它们能压过 <body>
		// 这类泛泛的外层包裹。
		if directLen := directTextLen(s); directLen >= 25 {
			get(s).score += 1.0 + math.Min(math.Floor(float64(directLen)/100.0), 3.0)
		}
	})

	// 按 (1 - 链接密度) 缩放：满是链接的页面得分会被显著压低。
	for _, c := range candidates {
		c.score *= 1.0 - linkDensity(c.sel)
	}
	return candidates
}

// pickTop 返回得分最高的候选。
func pickTop(candidates map[*html.Node]*scored) *scored {
	var top *scored
	for _, c := range candidates {
		if top == nil || c.score > top.score {
			top = c
		}
	}
	return top
}

// assembleArticle 返回一个新节点：包含最高分候选，外加它那些“也像正文”的兄弟
// 节点（即 Readability 的“兄弟合并”步骤），以免多段式正文被截断。对任意类型的
// 最高分节点都适用。
//
// 兄弟节点的保留规则（满足其一即保留）：
//   - 就是最高分节点本身；
//   - 自身也是高分候选（分数 ≥ 阈值，阈值 = max(10, 最高分*0.2)）；
//   - 是个像样的 <p> 段落：>80 字且链接密度 <0.25；或较短但“零链接且以句末
//     标点结尾”的完整短句。
func assembleArticle(top *scored, candidates map[*html.Node]*scored) *goquery.Selection {
	parent := top.sel.Parent()
	if parent.Length() == 0 || parent.Nodes[0] == nil {
		return wrapNodes([]*html.Node{top.sel.Nodes[0]})
	}

	threshold := math.Max(10, top.score*0.2)
	var nodes []*html.Node

	parent.Children().Each(func(_ int, sib *goquery.Selection) {
		keep := false
		switch {
		case sib.Nodes[0] == top.sel.Nodes[0]:
			keep = true
		case func() bool { c, ok := candidates[sib.Nodes[0]]; return ok && c.score >= threshold }():
			keep = true
		case nodeName(sib) == "p":
			txt := strings.TrimSpace(sib.Text())
			rl := lenRunes(txt)
			ld := linkDensity(sib)
			if rl > 80 && ld < 0.25 {
				keep = true
			} else if rl > 0 && rl <= 80 && ld == 0 && reSentenceEnd.MatchString(txt) {
				keep = true
			}
		}
		if keep {
			nodes = append(nodes, sib.Nodes[0])
		}
	})

	if len(nodes) == 0 {
		nodes = []*html.Node{top.sel.Nodes[0]}
	}
	return wrapNodes(nodes)
}

// wrapNodes 把给定节点深拷贝进一个新建的 <div>，再渲染成 HTML 并重新解析，
// 返回这个 <div> 的 selection。绕一圈 render+reparse 是为了得到一棵干净、
// 自洽的子树，方便后续 goquery 操作。
func wrapNodes(nodes []*html.Node) *goquery.Selection {
	container := newElement("div")
	for _, n := range nodes {
		if n != nil {
			container.AppendChild(cloneNode(n))
		}
	}
	var buf bytes.Buffer
	if err := html.Render(&buf, container); err != nil {
		return nil
	}
	doc, err := goquery.NewDocumentFromReader(&buf)
	if err != nil {
		return nil
	}
	return doc.Find("div").First()
}

// reSentenceEnd 判断文本是否以句末标点结尾（中英皆可），用于识别完整短句。
var reSentenceEnd = regexp.MustCompile(`[.!?。！？]\s*$`)

// reHierSep 匹配面包屑常用的层级分隔符（半/全角的 > / » › → | · 及书名号等）。
var reHierSep = regexp.MustCompile(`[>/»›→|·＞／\\]`)

// isBreadcrumbish 判断一个 p/div 是否“看起来像面包屑”：短、含 ≥2 个链接、链接密度高，
// 且文本里带层级分隔符。条件取交集，尽量只命中真正的面包屑而不误伤正常段落。
func isBreadcrumbish(s *goquery.Selection) bool {
	if s.Find("a").Length() < 2 {
		return false
	}
	txt := strings.TrimSpace(s.Text())
	rl := lenRunes(txt)
	if rl == 0 || rl > 120 { // 面包屑通常很短
		return false
	}
	if linkDensity(s) < 0.4 {
		return false
	}
	return reHierSep.MatchString(txt)
}

// cleanArticle 清掉“混进选定正文节点里”的残余垃圾。它会原地修改 article。
func cleanArticle(article *goquery.Selection) {
	// 1) 删除明显的非正文标签。
	article.Find("form, input, button, select, textarea, iframe, object, embed, footer, aside, nav").Remove()

	// 2) 删除残留进正文的强样板区块（评论/分享/相关推荐等）。
	article.Find("*").Each(func(_ int, s *goquery.Selection) {
		hint := strings.ToLower(attr(s, "class") + " " + attr(s, "id"))
		if strings.TrimSpace(hint) != "" && reBoiler.MatchString(hint) {
			s.Remove()
		}
	})

	// 3) 删除“链接密集”的区块（导航列表、标签云、相关/推荐列表、链接墙）。
	//    判定为垃圾的三种情形：
	//      a. 链接占比 >0.5 且总字数 <200（短而满是链接）；
	//      b. 链接占比 ≥0.65（压倒性是链接，不论长短，如专题聚合）；
	//      c. 链接 ≥8 个、占比 >0.4 且“句子标点比链接还少”（一堆链接、没几句话）。
	article.Find("ul, ol, div, section, nav").Each(func(_ int, s *goquery.Selection) {
		if len(s.Nodes) == 0 {
			return
		}
		ld := linkDensity(s)
		linkCount := s.Find("a").Length()
		runeLen := lenRunes(strings.TrimSpace(s.Text()))
		switch {
		case ld > 0.5 && runeLen < 200:
			s.Remove()
		case ld >= 0.65:
			s.Remove()
		case linkCount >= 8 && ld > 0.4 && punctCount(s.Text()) < linkCount:
			s.Remove()
		}
	})

	// 3.5) 面包屑：链接密集、带层级分隔符（> / » › 等）、且短的块。上面的链接密度清洗
	//      只扫容器标签（ul/ol/div/section/nav），漏掉了 <p> 形态的面包屑
	//      （如 <p class="bread-page">首页 > 栏目 > 当前页</p>）。这里对 p/div 单独判，
	//      条件严格（短 + ≥2 链接 + 高链接密度 + 含分隔符），避免误删带少量链接的正常段落。
	article.Find("p, div").Each(func(_ int, s *goquery.Selection) {
		if len(s.Nodes) > 0 && isBreadcrumbish(s) {
			s.Remove()
		}
	})

	// 4) 删除 class/id 带强负面信号的元素——但仅当它“不像真正的正文”时。
	//    一个文字充实、链接稀疏的块即使 id/class 恰好命中负面词（如
	//    id="same_scroll"）也会被保留。
	article.Find("*").Each(func(_ int, s *goquery.Selection) {
		hint := strings.ToLower(attr(s, "class") + " " + attr(s, "id"))
		if strings.TrimSpace(hint) == "" || rePositive.MatchString(hint) || !reNegative.MatchString(hint) {
			return
		}
		if lenRunes(strings.TrimSpace(s.Text())) < 100 || linkDensity(s) > 0.4 {
			s.Remove()
		}
	})

	// 5) 删除空的段落/容器（无文本且不含图片）。
	article.Find("p, div, span").Each(func(_ int, s *goquery.Selection) {
		if strings.TrimSpace(s.Text()) == "" && s.Find("img").Length() == 0 {
			s.Remove()
		}
	})

	// 6) 在剥属性之前，把懒加载的图片 URL 提升为真正的 src；找不到可用源就删掉。
	article.Find("img").Each(func(_ int, s *goquery.Selection) {
		if src := bestImageSrc(s); src != "" {
			s.SetAttr("src", src)
		} else {
			s.Remove()
		}
	})

	// 7) 剥掉表现性属性，只保留 href/src/alt/title/datetime，输出更干净。
	article.Find("*").Each(func(_ int, s *goquery.Selection) {
		n := s.Nodes[0]
		kept := n.Attr[:0] // 复用底层数组原地过滤，零额外分配
		for _, a := range n.Attr {
			switch a.Key {
			case "href", "src", "alt", "title", "datetime":
				kept = append(kept, a)
			}
		}
		n.Attr = kept
	})
}

// classScore 根据节点的 class 与 id 返回奖励/惩罚分。
func classScore(s *goquery.Selection) float64 {
	var score float64
	hint := attr(s, "class") + " " + attr(s, "id")
	if reNegative.MatchString(hint) {
		score -= 25
	}
	if rePositive.MatchString(hint) {
		score += 25
	}
	return score
}

// tagScore 根据元素类型返回基础分：语义越“像正文容器”的标签分越高，
// 列表/标题等“通常不是正文主体”的标签给负分。
func tagScore(s *goquery.Selection) float64 {
	switch nodeName(s) {
	case "article", "main":
		return 10
	case "section":
		return 8
	case "div":
		return 5
	case "blockquote", "pre", "td":
		return 3
	case "form", "ol", "ul", "dl", "dd", "dt", "li", "address":
		return -3
	case "h1", "h2", "h3", "h4", "h5", "h6", "th":
		return -5
	}
	return 0
}
