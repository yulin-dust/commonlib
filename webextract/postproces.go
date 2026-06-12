package webextract

import (
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

// defaultSkipPhrases 是开启短语过滤时使用的内置套话表。一个“句子”（按元素边界
// 与中文/ASCII 标点切分）只要包含其中任意短语（子串匹配），整句即被删除。
//
// 总体原则是尽量避开会误伤正文的单/双字泛词（如 "完"、"图"、"下载"、"关注"）——
// 它们会命中 "完成"、"关注度" 等正常词。表中保留的少数较激进的短词（如 "分享"、
// "收藏"）属于“宁可错杀”的取舍，请按业务容忍度斟酌；若需更激进的过滤，可通过
// Options.ExtraPhrases 自行追加。
//
// 该切片在初始化后只读，切勿修改（这样并发读取才安全）。
var defaultSkipPhrases = []string{
	// 推荐位 / 列表 / 互动入口
	"相关文章", "推荐阅读", "相关阅读", "往期回顾", "热门推荐",
	"收藏", "分享", "手机看", "【提醒",
	"猜你喜欢", "为您推荐", "延伸阅读", "更多精彩", "更多回答",

	// 编辑 / 记者 署名
	"责任编辑", "文字编辑", "图片编辑", "视频编辑", "网络编辑",
	"本文编辑", "文案编辑", "编辑部", "执行主编", "值班主编",
	"本报记者", "特约记者", "见习记者", "实习记者",
	"通讯员", "驻外记者", "前方记者", "随行记者",
	"译者", "审校", "撰文", "视觉设计", "文并摄",

	// 来源 / 出处
	"内容来源", "素材来源", "资料来源", "图片来源", "视频来源",
	"转载自", "转载请", "本文转载", "本文摘自", "本文来自",
	"综合整理", "综合报道", "综合编译",
	"原文链接", "原文地址", "原文标题", "首发于", "本文首发",

	// 图片 / 视频 署名
	"示意图", "资料图", "网络配图", "图片说明",
	"视觉中国", "东方IC", "新华社图",
	"视频制作", "视频剪辑", "后期制作",

	// 联系方式
	"联系我们", "联系电话", "联系方式", "联络方式",
	"投稿邮箱", "新闻热线", "爆料热线",
	"客服电话", "咨询电话", "热线电话", "服务热线",
	"商务合作", "合作请联系", "投稿请联系",

	// 版权 / 声明
	"版权所有", "版权声明", "著作权", "知识产权",
	"未经许可", "未经授权", "严禁转载", "禁止转载", "如需转载",
	"保留所有权利", "All Rights Reserved",
	"免责声明", "特别声明", "法律声明",
	"版权归原作者", "如有侵权请联系", "侵权删除",
	"图片来自网络", "图源网络", "内容仅供参考",
	"不构成投资建议", "不构成任何投资建议",

	// 结束语 / 页脚
	"以上内容", "特此声明", "特此说明", "敬请期待",
	"未完待续", "下期预告", "下回分解", "全文完毕",
	"展开全文", "收起全文", "点击加载更多",
	"返回搜狐", "返回网易", "返回腾讯", "返回新浪",
	"—END—", "- END -", "正文到此结束",

	// 时间标记
	"发布时间", "更新时间", "发稿时间", "刊发时间",

	// 提示语
	"温馨提示", "友情提示", "郑重声明", "重要提示", "特别提醒",

	// AI 特征词
	"AI生成", "AI创作", "AI写作", "AI辅助", "本文由AI", "AI整理", "AI摘要",
	"人工智能生成", "机器生成", "自动生成", "智能生成", "由人工智能",
	"机器人生成", "算法生成", "本文为推广信息",

	// 微信 - 关注 / 星标
	"点击上方蓝字", "点击下方蓝字", "点击下方关注", "长按二维码",
	"长按识别", "长按识别二维码", "扫码关注", "扫描二维码", "扫描下方二维码",
	"识别二维码", "识别下方二维码", "关注公众号", "关注我们", "关注本号",
	"设为星标", "加星标", "防失联", "防走丢", "以防失联",

	// 微信 - 在看 / 点赞 / 转发
	"点个在看", "点点在看", "请点在看", "顺手点个在看", "戳在看",
	"点个赞", "求点赞", "喜欢就点", "喜欢请点", "看完别忘了", "看完记得",
	"动动手指", "动动小手",

	// 微信 - 阅读原文 / 小程序
	"阅读原文", "查看原文", "点击阅读原文", "点击左下角",
	"点击文末", "戳左下角", "了解更多请点击",
	"进入小程序", "关注视频号",

	// 微信 - 群 / 客服 / 赞赏
	"加入读者群", "加入交流群", "加入微信群", "添加微信", "添加客服微信",
	"添加小编微信", "小编微信", "联系小编",
	"赞赏", "打赏", "支持原创", "请作者喝",
	"免费领取", "领取礼包", "活动详情", "活动规则", "兑换码",

	// 互动召唤（评论区 / 转发 / 分享）
	"欢迎留言", "欢迎评论", "留言区见", "评论区见", "留言告诉",
	"求转发", "求扩散", "转发朋友圈", "分享给朋友", "分享到朋友圈",

	// 总结性套话
	"总的来说", "总而言之", "综上所述", "综合来看", "综合分析",
	"归纳起来", "概括而言", "概括来说", "一言以蔽之",
	"最后总结", "最终总结", "由此可见",
}

// phrasesFor 返回本次调用应使用的短语表。基表按 AggressiveFilter 在「激进表」与
// 「默认表」之间二选一：没有 ExtraPhrases 时直接返回共享的只读基表（可安全并发
// 读取）；否则构造一份全新的合并切片（绝不修改全局表）。
func phrasesFor(o *Options) []string {
	base := defaultSkipPhrases
	if o.AggressiveFilter {
		base = aggressiveSkipPhrases
	}
	if len(o.ExtraPhrases) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(o.ExtraPhrases))
	out = append(out, base...)
	out = append(out, o.ExtraPhrases...)
	return out
}

// splitSentences 把文本切成句子，分隔标点保留在句尾（中文/ASCII 句读 + 换行）。
// 由于连逗号也作为切分点，过滤时能做到“只删命中套话的那一小句、保留其余”。
func splitSentences(s string) []string {
	var out []string
	var b strings.Builder
	for _, r := range s {
		b.WriteRune(r)
		switch r {
		case '。', '！', '？', '；', '，', '、', '\n',
			'!', '?', ';', ',':
			out = append(out, b.String())
			b.Reset()
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

// containsAnyPhrase 报告 s 是否包含 phrases 中的任意一个（子串匹配）。
func containsAnyPhrase(s string, phrases []string) bool {
	for _, p := range phrases {
		if p != "" && strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// reLabelColon 匹配“1~4 个字的短标签 + 冒号”的句首，例如「来源：」「整理：」
// 「编辑：」「记者：」「图：」「文/图：」等。冒号支持半角 ":" 与全角 "："。
// [^\s:：]{1,4} 限定标签为 1~4 个非空白、非冒号字符（按 rune 计，中文同样适用）。
var reLabelColon = regexp.MustCompile(`^[^\s:：]{1,4}[:：]`)

// isLabelColon 判断一句话是否以“短标签 + 冒号”开头（如 来源：/整理：/编辑：……），
// 这类多为署名/出处/图注等元信息，应整句剔除。
func isLabelColon(s string) bool {
	return reLabelColon.MatchString(strings.TrimSpace(s))
}

// dropSkipPhraseSentences 删除“含有套话短语”或“以短标签+冒号开头”的整句，保留同一
// 文本节点中的其余句子。文本本身就是单句/片段（无句读标点）时，命中即整体删除、
// 否则原样返回。
func dropSkipPhraseSentences(text string, phrases []string) string {
	if !strings.ContainsAny(text, "。！？；，、,!?;\n") {
		// 单句/片段：命中套话或形如“来源：xxx”的短标签则整体删除。
		if containsAnyPhrase(text, phrases) || isLabelColon(text) {
			return ""
		}
		return text
	}
	var b strings.Builder
	for _, sent := range splitSentences(text) {
		if containsAnyPhrase(sent, phrases) || isLabelColon(sent) {
			continue
		}
		b.WriteString(sent)
	}
	return b.String()
}

// filterPhrasesInNode 遍历 sel 子树下的每个文本节点，删除其中命中套话的句子。
// 在 DOM 层面就地改写文本，能保证最终的 Text / ContentHTML / Markdown 三者一致。
func filterPhrasesInNode(sel *goquery.Selection, phrases []string) {
	if sel == nil || len(sel.Nodes) == 0 {
		return
	}
	// 先收集全部文本节点再统一处理，避免在遍历途中修改树结构。
	var texts []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n == nil {
			return
		}
		if n.Type == html.TextNode {
			texts = append(texts, n)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, root := range sel.Nodes {
		walk(root)
	}
	for _, tn := range texts {
		if strings.TrimSpace(tn.Data) == "" {
			continue
		}
		tn.Data = dropSkipPhraseSentences(tn.Data, phrases)
	}
}

// mediaTags 是开启 Options.StripMedia 时要删除的媒体标签集合。
var mediaTags = "img, picture, source, video, audio, iframe, embed, object, svg, canvas"

// stripMedia 从正文节点中删除所有媒体元素。
func stripMedia(sel *goquery.Selection) {
	if sel == nil {
		return
	}
	sel.Find(mediaTags).Remove()
}

// stripMediaFromHTML 解析一段正文 HTML，产出 Article.ContentHTMLNoMedia——“仅含
// 文本的 HTML”。它做两件事：
//   - 删除媒体元素（img/video/iframe/svg 等，连同其内容）；
//   - 拆掉 <a> 链接外壳，但保留其文字（unwrap：去掉标签与 href，文字仍是正文内容）。
//
// 再清理因此变空的容器。无论调用方是否设置 Options.StripMedia，都能额外拿到这份
// 纯文字版正文（图片仍可从 Article.Images 取得）。
func stripMediaFromHTML(fragment string) string {
	if strings.TrimSpace(fragment) == "" {
		return ""
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(fragment))
	if err != nil {
		return fragment
	}
	body := doc.Find("body")
	if body.Length() == 0 {
		return ""
	}
	stripMedia(body)  // 媒体：整体删除
	unwrapLinks(body) // 链接：拆壳留字
	removeEmpty(body)
	return htmlOf(body)
}

// unwrapLinks 把 sel 子树里的每个 <a> 拆掉外壳：用其子节点（文字/行内内容）原地
// 替换该 <a>，从而去掉标签与 href，但保留链接文字。
func unwrapLinks(sel *goquery.Selection) {
	if sel == nil {
		return
	}
	// 先收集所有 <a> 节点再统一处理，避免遍历途中改树。子 <a> 的 Parent 在父 <a>
	// 被拆后会更新到祖先，unwrapNode 用调用时的 Parent，仍然正确。
	sel.Find("a").Each(func(_ int, s *goquery.Selection) {
		if len(s.Nodes) > 0 {
			unwrapNode(s.Nodes[0])
		}
	})
}

// unwrapNode 用 n 的全部子节点原地替换 n：把子节点依次插到 n 之前，再摘掉 n。
func unwrapNode(n *html.Node) {
	parent := n.Parent
	if parent == nil {
		return
	}
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		n.RemoveChild(c)
		parent.InsertBefore(c, n)
		c = next
	}
	parent.RemoveChild(n)
}

// keepEmpty 是“即便没有文本也有意义”的元素（空元素/媒体），它们永不因为“空”
// 而被 removeEmpty 删除。
var keepEmpty = map[string]bool{
	"img": true, "br": true, "hr": true, "video": true, "audio": true,
	"iframe": true, "embed": true, "object": true, "source": true,
	"svg": true, "canvas": true, "picture": true, "wbr": true, "col": true,
}

// removeEmpty 删除“既无文本也无媒体”的容器元素。删除媒体或剔除套话后，常会留下
// 一批被掏空的 <div>/<p>，此函数把它们一并清掉。
//
// 实现上用一次自底向上（后序）的遍历完成：子节点先于父节点判定，因此“父节点因
// 子节点被删而变空”的情况会在同一趟里被自然处理——无需像早期实现那样反复扫描
// 直到稳定（原先最多 8 趟 Find("*")，这里是单趟 O(n)，语义等价）。
func removeEmpty(sel *goquery.Selection) {
	if sel == nil {
		return
	}
	for _, root := range sel.Nodes {
		pruneEmpty(root)
	}
}

// pruneEmpty 后序遍历以 n 为根的子树，删除其中“空”的可删元素，并返回该子树是否
// 仍含有“有意义的内容”（非空文本，或 keepEmpty 媒体元素），供父节点判断自身去留。
// 注意：根节点 n 自身不会在这里被删除（只删它的子节点）。
func pruneEmpty(n *html.Node) bool {
	if n.Type == html.TextNode {
		return strings.TrimSpace(n.Data) != ""
	}

	meaningful := false
	var next *html.Node
	for c := n.FirstChild; c != nil; c = next {
		next = c.NextSibling // 先存好下一个，因为 c 可能被摘除
		if pruneEmpty(c) {
			meaningful = true
			continue
		}
		// 子节点无内容：可删元素则摘掉；keepEmpty 元素（如 <img>）保留并视为有意义；
		// 空白文本节点保持原样（不删，也不计为有意义）。
		if c.Type == html.ElementNode {
			if keepEmpty[strings.ToLower(c.Data)] {
				meaningful = true
			} else {
				n.RemoveChild(c)
			}
		}
	}
	return meaningful
}
