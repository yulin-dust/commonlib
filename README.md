# commonlib

一组自用的 Go 公共库。目前包含两个相对独立的包：

| 包 | 作用 | 是否 cgo |
|---|---|---|
| [`lclient`](./lclient) | 带**完整浏览器 TLS/HTTP2 指纹模拟**的 HTTP 客户端（Python `requests` 风格），内置身份轮换、连接池、共享 DNS/TLS 缓存、SSRF 防护、响应体闸、可中断 context、自动字符集解码等 | ✅ 需要原生库 |
| [`webextract`](./webextract) | 纯 Go 的网页正文 + 元数据提取（对标 trafilatura/readability），输出纯文本 / Markdown / 仅含文本的 HTML，字段对齐 go-trafilatura，针对中文优化 | ❌ 纯 Go |

## 安装

```bash
go get github.com/yulin-dust/commonlib
```

按需 import 对应子包即可：

```go
import "github.com/yulin-dust/commonlib/lclient"
import "github.com/yulin-dust/commonlib/webextract"
```

> **按包编译**：Go 只编译你 import 的包及其依赖。**只用 `webextract`（纯 Go）时不会触发 cgo**，
> 无需安装任何原生库；只有 import `lclient` 才需要下面的 `libcurl-impersonate`。

### lclient 的原生依赖（仅当使用 lclient）

`lclient` 是 cgo，底层依赖原生库 `libcurl-impersonate`（fork 已内置在
[`third_party/curlimpersonate`](./third_party/curlimpersonate)，**无需额外 replace**）。
装好原生库后 `pkg-config --exists libcurl-impersonate` 应当成功。安装见：

- macOS：[`lclient/README-mac 安装.md`](./lclient/README-mac%20安装.md)
- Linux：[`lclient/README-linux 安装.md`](./lclient/README-linux%20安装.md)
- 不支持 Windows。

## 快速开始

### 抓取（lclient）

```go
s := lclient.NewSession(
    lclient.WithRandomIdentity(),  // 每请求随机一套自洽浏览器身份（TLS+UA+headers）
    lclient.WithConnectionPool(8), // 连接复用
    // autoDecode 默认开启：GB2312/GBK/Big5 等自动转 UTF-8
)
defer s.Close()

resp, err := s.Get("https://example.com")
fmt.Println(resp.StatusCode, resp.Text())
```

### 解析正文（webextract）

```go
art, err := webextract.FromString(htmlStr, &webextract.Options{
    PageURL: "https://example.com/post",
})
fmt.Println(art.Title, art.Text)        // 纯文本
fmt.Println(art.Markdown)               // Markdown
fmt.Println(art.ContentHTMLNoMedia)     // 仅含文本的 HTML
```

### 抓 + 解（注意字符集）

把 lclient 抓的 body 交给 webextract 时，**用原始 `resp.Body` 并关掉 lclient 的自动解码**，
让 webextract 自己处理字符集（否则会按页面里的 `<meta charset>` 二次解码而损坏内容）：

```go
s := lclient.NewSession(lclient.WithAutoDecode(false)) // 关掉自动解码
resp, _ := s.Get(pageURL)
art, _ := webextract.FromReader(bytes.NewReader(resp.Body), &webextract.Options{PageURL: pageURL})
```

## 各包文档

- lclient：[`lclient/README.md`](./lclient/README.md)
- webextract：[`webextract/README.md`](./webextract/README.md)
- 可运行示例：[`example/lclient`](./example/lclient)、[`example/trafilatura`](./example/trafilatura)

## 测试

```bash
go test ./...                 # 离线单测（含 -race 友好）
LCLIENT_LIVE=1 go test ./lclient/ -run Live   # 联网用例（默认跳过）
```

> CI 注意：跑到 `lclient` / `third_party/curlimpersonate` 的测试需要 runner 上装好
> `libcurl-impersonate`；只测 `webextract` 则无此要求。
