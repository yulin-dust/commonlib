# webextract

纯 Go 实现的网页正文 + 元数据提取库，对标 Python 的 trafilatura / readability / newspaper / goose。
适合**通用全网爬取**：丢进一段 HTML（或一个 URL），吐出标题、作者、发布时间、正文、配图等结构化数据。针对**中文页面**做了专门优化。

## 安装 / 运行

```bash
cd webextract
go mod tidy          # 拉取 goquery + golang.org/x/net
go test ./...        # 跑内置测试（含中文样本）
go build -o extract ./cmd/extract
```

> 说明：随附的 `go.mod` 是标准写法。如果你的环境无法访问 `proxy.golang.org`，
> 自行配置 `GOPROXY`（如 `https://goproxy.cn`）即可。

## 用法

作为库：

```go
import "github.com/example/webextract"

// 从 URL 抓取并解析
art, err := webextract.FromURL("https://news.example.com/article", nil)

// 或从已有 HTML 解析（推荐：抓取与解析解耦，便于配合代理/重试/JS 渲染）
art, err := webextract.FromString(htmlString, &webextract.Options{
    PageURL: "https://news.example.com/article", // 用于把相对链接/图片转成绝对地址
})

fmt.Println(art.Title, art.Author, art.PublishDate)
fmt.Println(art.Text)        // 纯文本正文
fmt.Println(art.ContentHTML) // 清洗后的正文 HTML
```

命令行：

```bash
./extract https://news.example.com/article          # 抓取并输出 JSON
cat page.html | ./extract -url https://news.example.com/article
cat page.html | ./extract -text -url https://...     # 只输出标题+正文
cat page.html | ./extract -md   -url https://...     # 输出 Markdown 文档
```

## 输出字段（Article）

字段命名对齐 [go-trafilatura](https://github.com/markusmobius/go-trafilatura) 的 `Metadata`，并额外提供 Markdown 与"仅含文本的 HTML"两种产物。

### 三种正文文本表示（最常用）

| 字段 | 说明 |
|---|---|
| `Text` | **纯文本**正文，段落以空行分隔 |
| `Markdown` | 正文渲染成的 **Markdown**（标题/段落/链接/图片/列表/引用/代码，链接已绝对化）|
| `ContentHTMLNoMedia` | **仅含文本的 HTML**：去除 `<img>/<video>/<iframe>/<svg>` 等媒体，并拆掉 `<a>` 链接外壳（去标签与 href、保留链接文字），只留纯文字结构（图片仍可从 `Images` 取）|

### 元信息（对齐 trafilatura `Metadata`）

| 字段 | 说明 |
|---|---|
| `Title` | 标题（og:title > twitter > JSON-LD > `<title>`(去站名后缀) > `<h1>`）|
| `Author` | 作者（meta/JSON-LD/byline，多作者用逗号连接）|
| `URL` / `CanonicalURL` / `Hostname` | 源 URL、规范链接、主机名（去 `www.`）|
| `PublishDate` / `ParsedDate` | 原始时间字符串 + 解析出的 `time.Time`（含 `/2026/05/30/` 这类 **URL 路径日期**兜底）|
| `Description` / `Excerpt` | 摘要（meta description，缺失则取正文前 200 字）|
| `SiteName` / `Language` / `PageType` | 站点名、语言、页面类型（og:type）|
| `Categories` / `Tags` / `Keywords` | 栏目（article:section + 面包屑）/ 标签（article:tag、rel=tag、news_keywords）/ meta 关键词 |
| `License` | 内容许可（rel=license 文本，或 Creative Commons 链接识别为 `CC BY-SA 4.0` 之类）|
| `TopImage` / `Images` / `Favicon` | 主图 + 正文内图片（含懒加载，自动转绝对地址）+ 站点图标 |
| `Fingerprint` | 正文内容指纹（归一化后 FNV-1a 哈希），用于跨页**去重**（识别转载/站点样板）|

### 其它正文产物

| 字段 | 说明 |
|---|---|
| `ContentHTML` | 完整正文 HTML（保留图片，链接/图片已绝对化）|
| `MarkdownNoMedia` | 去媒体后的 Markdown，与 `ContentHTMLNoMedia` 对应 |
| `WordCount` | 词数（CJK 按字计，其余按词计）|

## 实现原理（对应原库的能力）

- **元数据提取**（≈ newspaper / trafilatura 的 metadata）：`metadata.go`
  统一扫描 `<meta>`（标准 + OpenGraph + Twitter Card + Dublin Core），解析 `application/ld+json`
  （支持顶层数组与 `@graph`），再叠加 `<time datetime>`、`rel=author`、canonical、favicon 等兜底。

- **正文提取**（≈ readability / goose 的 Readability 算法）：`content.go`
  1. 预处理：删除 `script/style/nav/footer/aside/form` 等，以及 class/id 命中「不像正文」正则的容器；
  2. 打分：对段落级节点（`p/td/pre/...`）按文本长度 + 标点数量打分，分数累加到父/祖父容器，
     再用 class/id 正向(`article|content|post...`)/负向(`comment|sidebar|ad...`)正则加减分；
  3. 链接密度惩罚：`score *= (1 - 链接字符占比)`，过滤导航/标签云/相关阅读；
  4. 选出最高分容器，按 Readability 的兄弟节点合并策略补齐多 `div` 正文；
  5. 清洗：去残留噪声、空节点、懒加载图片转真实 `src`、剥离展示性属性。

- **中文优化**：长度按 `rune` 计而非字节；标点计数纳入 `，。、；！？：` 等中文标点；
  词数统计对汉字/假名/谚文逐字计。

## 套话过滤（清掉署名 / 版权 / 关注引导等）

按句切分后，整句命中短语表即删除（子串匹配），三档强度：

```go
// 1) 保守：内置 defaultSkipPhrases（避开单字泛词，误伤低）
webextract.FromString(html, &webextract.Options{FilterPhrases: true})

// 2) 追加自定义短语（FilterPhrases 自动视为开启）
webextract.FromString(html, &webextract.Options{ExtraPhrases: []string{"本台记者"}})

// 3) 激进：改用 aggressiveSkipPhrases（含 关注/分享/图/来源/编辑/记者/广告/http 等
//    单字双字泛词），适合喂给下游模型、对纯净度要求高的场景
webextract.FromString(html, &webextract.Options{AggressiveFilter: true})
```

> ⚠ `AggressiveFilter` 是「宁可错杀」：子串匹配下 "图" 会命中 "地图"、"关注" 会命中
> "关注度"。对纯净度不敏感的场景请用 `FilterPhrases`。`ExtraPhrases` 在两档下都会叠加。
> 过滤在 DOM 层就地改写，保证 `Text` / `Markdown` / `ContentHTML*` 三者一致。

## 已知边界（与原 Python 库一致）

- **不渲染 JavaScript**。输入是静态 HTML。遇到前端渲染/Ajax 加载的页面，
  先用 chromedp / rod（无头浏览器）拿到渲染后的 HTML，再交给 `FromString`。
- 启发式算法没有 100% 准确率；版式极端的页面可能漏段或带噪。
  如需进一步提升，可针对目标站点加专属的 class/id 规则（类似 GNE 的 `noise_node_list`）。

## 文件结构

```
extractor.go        # 公共 API、Article 结构、流程编排
metadata.go         # 元数据 + JSON-LD 解析
content.go          # Readability 式正文打分与清洗
markdown.go         # 正文 HTML → Markdown 转换（自包含，无额外依赖）
helpers.go          # 文本/链接密度/URL/节点工具
extractor_test.go   # 测试（中文新闻样本）
cmd/extract/main.go # CLI
```

## 作为公共库 / 本地使用

这个包的公共 API 已经是导出的（`FromURL` / `FromString` / `FromReader` / `Article` / `Options`），
拿来即用。关键只有一件事：**模块路径（go.mod 里的 `module`）必须等于别人 import 时用的路径**。
当前是占位的 `github.com/example/webextract`，改成你自己的即可。

### 方案 A：作为独立的公共仓库

```bash
# 1. 改模块路径为你的仓库地址
go mod edit -module github.com/yourname/webextract

# 2. 同步改掉 cmd/extract/main.go 里的 import
#    github.com/example/webextract  ->  github.com/yourname/webextract
sed -i 's#github.com/example/webextract#github.com/yourname/webextract#' cmd/extract/main.go

go mod tidy

# 3. 推到 GitHub 并打版本 tag（Go 用 semver tag 做版本）
git init && git add . && git commit -m "init webextract"
git remote add origin git@github.com:yourname/webextract.git
git push -u origin main
git tag v0.1.0 && git push origin v0.1.0
```

别人（或你别的项目）这样用：

```bash
go get github.com/yourname/webextract@v0.1.0
# CLI 也能直接装：
go install github.com/yourname/webextract/cmd/extract@latest
```

```go
import "github.com/yourname/webextract"

art, _ := webextract.FromURL("https://...", nil)
```

> 注意：升级到不兼容的 v2 时，模块路径要带后缀 `github.com/yourname/webextract/v2`。

### 方案 B：放进你已有的库仓库里当一个子包

如果你已经有一个公共库仓库（比如 `module github.com/yourco/commonlib`），
想把它作为其中一个包：

1. 把 `*.go` 源文件放到 `commonlib/webextract/` 目录下（保持 `package webextract`）。
2. **删掉本包自己的 `go.mod`**——一个仓库（模块）里不要再嵌套 go.mod，否则它会变成独立模块。
3. 在 `commonlib` 根目录跑 `go mod tidy`，把 goquery / x/net 合并进父模块的依赖。
4. import 路径就是 `github.com/yourco/commonlib/webextract`。

### 本地开发（还没发布 / 想边改边用）

**同一个模块内**：直接按包路径 import，无需任何额外操作。

**跨模块、用本地路径调试**——两种标准做法：

1. `replace` 指令（在「使用方」项目的 go.mod 里）：

   ```
   require github.com/yourname/webextract v0.0.0
   replace github.com/yourname/webextract => ../webextract   // 指向本地目录
   ```
   然后 `go mod tidy`。发布使用方前记得把 replace 去掉。

2. Go Workspace（推荐，Go 1.18+，不污染 go.mod）：

   ```bash
   # 假设本地有 ./webextract 和 ./myapp 两个模块
   go work init ./webextract ./myapp
   ```
   之后 `myapp` 里 import `github.com/yourname/webextract` 会自动解析到本地目录，
   不需要 replace。`go.work` 一般加进 .gitignore，只用于本地。

