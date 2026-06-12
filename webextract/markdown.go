package webextract

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

var (
	reInlineWS   = regexp.MustCompile(`\s+`)    // 行内连续空白 → 单空格
	reMultiBlank = regexp.MustCompile(`\n{3,}`) // 连续空行 → 单个空行
)

// toMarkdown 把（extractContent 产出的）清洗后 HTML 片段转换成 Markdown。
//
// 它有意做得很小：只针对“经过正文清洗后还会留下的元素集合”——标题、段落、
// 链接、图片、列表、引用、代码、强调——而非一个通用的 HTML→Markdown 转换器。
func toMarkdown(fragment string) string {
	if strings.TrimSpace(fragment) == "" {
		return ""
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(fragment))
	if err != nil {
		return ""
	}
	body := doc.Find("body")
	if body.Length() == 0 {
		return ""
	}
	var b strings.Builder
	renderContainer(&b, body.Nodes[0])
	return tidyMarkdown(b.String())
}

// isBlockElement 判断节点是否为需要单独成块渲染的块级元素。
func isBlockElement(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	switch strings.ToLower(n.Data) {
	case "p", "h1", "h2", "h3", "h4", "h5", "h6", "ul", "ol", "blockquote",
		"pre", "figure", "figcaption", "div", "section", "article", "hr", "table":
		return true
	}
	return false
}

// renderContainer 遍历一个块级容器：把散落的行内内容聚成段落，把块级子元素
// 交给 renderBlock 分派渲染。
func renderContainer(b *strings.Builder, n *html.Node) {
	var inlineBuf strings.Builder
	// flush 把已积累的行内内容作为一个段落输出（前后各空一行）。
	flush := func() {
		s := strings.TrimSpace(inlineBuf.String())
		if s != "" {
			b.WriteString("\n\n")
			b.WriteString(s)
			b.WriteString("\n\n")
		}
		inlineBuf.Reset()
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if isBlockElement(c) {
			flush() // 遇到块级元素前，先把攒着的行内内容收尾成段
			renderBlock(b, c)
		} else {
			inlineBuf.WriteString(renderInline(c))
		}
	}
	flush()
}

// renderBlock 渲染单个块级元素（标题/段落/引用/代码块/列表/分隔线等）。
func renderBlock(b *strings.Builder, n *html.Node) {
	name := strings.ToLower(n.Data)
	switch name {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		level := int(name[1] - '0') // 'h2' → 2
		text := strings.TrimSpace(renderChildrenInline(n))
		if text != "" {
			b.WriteString("\n\n" + strings.Repeat("#", level) + " " + text + "\n\n")
		}
	case "p":
		if text := strings.TrimSpace(renderChildrenInline(n)); text != "" {
			b.WriteString("\n\n" + text + "\n\n")
		}
	case "figcaption":
		// 图注用斜体表示。
		if text := strings.TrimSpace(renderChildrenInline(n)); text != "" {
			b.WriteString("\n\n*" + text + "*\n\n")
		}
	case "blockquote":
		// 先把引用内部渲染好，再逐行加上 "> " 前缀。
		var sub strings.Builder
		renderContainer(&sub, n)
		for _, line := range strings.Split(strings.TrimSpace(sub.String()), "\n") {
			if strings.TrimSpace(line) == "" {
				b.WriteString("\n>")
			} else {
				b.WriteString("\n> " + line)
			}
		}
		b.WriteString("\n\n")
	case "pre":
		// 代码块用三反引号围栏，内部取纯文本。
		b.WriteString("\n\n```\n" + strings.TrimRight(textContent(n), "\n") + "\n```\n\n")
	case "ul", "ol":
		b.WriteString("\n")
		renderList(b, n, 0, name == "ol")
		b.WriteString("\n")
	case "hr":
		b.WriteString("\n\n---\n\n")
	default: // figure / div / section / article / table：作为容器继续向下渲染
		renderContainer(b, n)
	}
}

// renderList 渲染列表。depth 为嵌套深度（控制缩进），ordered 表示有序列表。
func renderList(b *strings.Builder, list *html.Node, depth int, ordered bool) {
	idx := 0
	for c := list.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || strings.ToLower(c.Data) != "li" {
			continue
		}
		idx++
		indent := strings.Repeat("  ", depth)
		marker := "- "
		if ordered {
			marker = fmt.Sprintf("%d. ", idx)
		}
		if text := strings.TrimSpace(renderLIInline(c)); text != "" {
			b.WriteString("\n" + indent + marker + text)
		}
		// 嵌套子列表：递归渲染，深度 +1。
		for cc := c.FirstChild; cc != nil; cc = cc.NextSibling {
			if cc.Type == html.ElementNode {
				if nm := strings.ToLower(cc.Data); nm == "ul" || nm == "ol" {
					renderList(b, cc, depth+1, nm == "ol")
				}
			}
		}
	}
}

// renderLIInline 渲染列表项的行内内容，跳过嵌套子列表（那由 renderList 单独处理）。
func renderLIInline(li *html.Node) string {
	var b strings.Builder
	for c := li.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			if nm := strings.ToLower(c.Data); nm == "ul" || nm == "ol" {
				continue
			}
		}
		b.WriteString(renderInline(c))
	}
	return b.String()
}

// renderInline 渲染行内节点：文本、换行、链接、图片、强调、行内代码等。
func renderInline(n *html.Node) string {
	switch n.Type {
	case html.TextNode:
		return reInlineWS.ReplaceAllString(n.Data, " ")
	case html.ElementNode:
		switch strings.ToLower(n.Data) {
		case "br":
			return "\n"
		case "a":
			inner := strings.TrimSpace(renderChildrenInline(n))
			href := getAttr(n, "href")
			if href == "" || inner == "" { // 无链接或无文字，退化成纯文本
				return inner
			}
			return "[" + inner + "](" + href + ")"
		case "img":
			src := getAttr(n, "src")
			if src == "" {
				return ""
			}
			return "![" + getAttr(n, "alt") + "](" + src + ")"
		case "strong", "b":
			if inner := strings.TrimSpace(renderChildrenInline(n)); inner != "" {
				return "**" + inner + "**"
			}
			return ""
		case "em", "i":
			if inner := strings.TrimSpace(renderChildrenInline(n)); inner != "" {
				return "*" + inner + "*"
			}
			return ""
		case "code":
			return "`" + textContent(n) + "`"
		default: // 其它行内标签：透传其子节点
			return renderChildrenInline(n)
		}
	}
	return ""
}

// renderChildrenInline 依次渲染节点的所有子节点（行内）。
func renderChildrenInline(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(renderInline(c))
	}
	return b.String()
}

// textContent 递归取节点子树里的全部文本（不做任何 Markdown 转义）。
func textContent(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// getAttr 返回节点指定属性的去空白值，不存在则返回空串。
func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}

// tidyMarkdown 收尾整理：连续空行收敛成单个空行，去掉每行尾部空白，并整体 Trim。
func tidyMarkdown(s string) string {
	s = reMultiBlank.ReplaceAllString(s, "\n\n")
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(ln, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
