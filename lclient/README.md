# lclient

基于 [`go-curl-impersonate`](https://github.com/TeamMilestone/go-curl-impersonate)（底层
[`libcurl-impersonate`](https://github.com/lexiforest/curl-impersonate)）的 Go HTTP 客户端，
提供 **Python `requests` 风格** 的 API + **完整浏览器 TLS / HTTP2 指纹模拟**，并在此基础上做了
一层面向爬虫的工程加固：浏览器身份轮换、连接复用、跨句柄共享缓存、SSRF 防护、响应体闸、
真正可中断的 context 取消等。

适合需要绕过大型电商 / Cloudflare / Akamai / DataDome / 阿里风控等高级反爬的场景。

---

## 平台支持

| 平台 | 支持 |
|---|---|
| macOS (x86_64 / arm64) | ✅ |
| Linux (glibc / musl, x86_64 / arm64) | ✅ |
| Windows | ❌ 暂不支持 |

## 安装（先装原生库，否则编译不过）

本包是 **cgo**，依赖原生库 `libcurl-impersonate`（以及 `libidn2` / `zstd` 等）。装好原生库后，
`pkg-config --exists libcurl-impersonate` 应当成功。按平台看对应指南：

- macOS：[`README-mac 安装.md`](./README-mac%20安装.md)
- Linux：[`README-linux 安装.md`](./README-linux%20安装.md)

> **依赖说明（重要）**：lclient 依赖的 `go-curl-impersonate` 是一个**本地 fork**，在
> `commonlib/go.mod` 里通过 `replace` 指向 `./third_party/curlimpersonate`。因此把 lclient
> 作为外部依赖 `go get` 时，使用方的 `go.mod` 也要带上同样的 `require` + `replace`，把 fork
> 一并提供。直接在 `commonlib` 仓库内使用则无需任何额外配置。

---

## 快速开始

```go
import "github.com/yulin-dust/commonlib/lclient"

s := lclient.NewSession(
    lclient.WithImpersonate(lclient.ChromeLatest), // TLS/HTTP2 指纹
)
defer s.Close()

resp, err := s.Get("https://example.com")
if err != nil {
    log.Fatal(err)
}
fmt.Println(resp.StatusCode, len(resp.Body))
fmt.Println(resp.Text())
```

POST / JSON / 表单：

```go
resp, _ := s.Post("https://api.example.com/login",
    lclient.JSON(map[string]any{"user": "a", "pass": "b"}),
)

resp, _ = s.Post("https://api.example.com/form",
    lclient.Form(map[string]string{"k": "v"}),
)

var out struct{ Token string `json:"token"` }
_ = resp.JSONUnmarshal(&out)
```

请求级选项（query / header / 超时 / 代理 / profile 等）按需叠加：

```go
resp, _ := s.Get("https://example.com/search",
    lclient.Params{"q": "中文", "page": "1"},
    lclient.Headers{"Referer": "https://example.com"},
    lclient.Timeout(10*time.Second),
    lclient.Proxy("socks5h://user:pass@host:1080"),
    lclient.Impersonate(lclient.Safari18),
)
```

---

## 反爬 / 工程加固特性

下面这些是在原始指纹模拟之上叠加的能力，多数是 `NewSession` 的选项。

### 1. 浏览器身份轮换 `WithRandomIdentity`

每次请求随机选用一套**自洽**的浏览器身份（TLS profile + 匹配的 UA + `sec-ch-ua` + 正确的
请求头顺序）。这比"随机打乱 header / 随机拼 UA"更可靠——后者会制造 UA 与指纹不一致的矛盾，
反而更易被识别。

```go
s := lclient.NewSession(
    lclient.WithRandomIdentity(
        lclient.WithRandomAcceptLanguage(),                  // 顺带随机 Accept-Language
        // lclient.WithIdentityPool(lclient.ChromeIdentities()...), // 限定只在 Chrome 间轮换
    ),
)
```

单次请求强制指定身份：`s.Get(url, lclient.UseIdentity(id))`。

### 2. 连接复用 `WithConnectionPool`

复用底层 curl 句柄，保留其连接缓存、TLS 会话缓存、DNS 缓存，省掉重复 TCP + TLS 握手。
对同一批站点高频抓取提速明显（实测同站后续请求约 3×）。

```go
s := lclient.NewSession(lclient.WithConnectionPool(8)) // 保留至多 8 个空闲句柄
```

> 与 `WithRandomIdentity` 同用时，不同身份 TLS 参数不同，curl 可能不复用为另一身份建立的连接，
> 复用率会下降（不影响正确性）。要最大化复用就固定单一身份。

### 3. 跨句柄共享 DNS / TLS 会话缓存 `WithSharedCache`

让所有句柄（乃至无连接池时每请求新建的临时句柄）共用一份 DNS 解析与 TLS 会话票据：DNS 不必
重复解析，新建连接也能走 TLS resumption。与连接池互补，建议搭配使用。

```go
s := lclient.NewSession(
    lclient.WithConnectionPool(8),
    lclient.WithSharedCache(),
)
```

### 4. SSRF 防护 `WithBlockPrivateIPs`

当目标域名**解析到**私网 / 环回 / 链路本地地址（`127.0.0.1`、`10/8`、`192.168/16`、
`169.254.169.254` 云元数据、`::1` 等）时，在连接后、发请求前中止。因为是在 IP 解析之后判定，
**能挡 DNS rebinding，也能挡重定向跳内网**。爬取不可信 URL 时强烈建议开启。

```go
s := lclient.NewSession(lclient.WithBlockPrivateIPs(true))
_, err := s.Get(untrustedURL)
if errors.Is(err, lclient.ErrBlockedAddress) { /* 命中内网，已拦截 */ }
```

> 限制：该检查基于"实际连接到的 IP"，只对**直连**有效。请求经代理（显式 `WithProxy` 或命中
> `http_proxy`/`https_proxy`/`all_proxy` 且不在 `no_proxy`）时会自动跳过——此时连的是代理、
> 由代理解析目标，按连接 IP 判定既会误杀也保护不到目标。经代理的出口管控应在代理侧做。

### 5. 响应体大小闸 `WithMaxResponseBytes`

限制单次响应体最大字节数（按**解压后**计），防解压炸弹 / 误抓超大资源把内存吃满。

```go
s := lclient.NewSession(lclient.WithMaxResponseBytes(10 << 20)) // 10 MiB
_, err := s.Get(url)
if errors.Is(err, lclient.ErrBodyTooLarge) { /* 超限，已中止 */ }
```

也可单次覆盖：`s.Get(url, lclient.MaxResponseBytes(1<<20))`。

### 6. 可中断的 context 取消

`ctx` 取消 / 超时会中断重试等待、限流等待，并通过 curl 进度回调**中止在途的 HTTP 传输**
（无数据流动时约每秒一次粒度，故通常 1 秒内返回，不必干等 curl 内部超时）。

```go
ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
defer cancel()
_, err := s.Get(url, lclient.Ctx(ctx))
if errors.Is(err, lclient.ErrCanceled) { /* 被取消 / 超时中止 */ }
```

### 7. 其它已内建的加固（无需配置）

- **仅允许 http/https**：默认禁掉 `file://`/`scp://`/`gopher://` 等，主请求与重定向都限制，
  杜绝本地文件读取 / 协议级 SSRF。
- **CRLF header 注入防护**：发送前拒绝键/值含 CR/LF 等控制字符的请求（`ErrInvalidHeader`）。
- **TCP keepalive**、**自动解压** gzip/deflate/br/zstd、**默认校验目标 TLS 证书**。

---

## 重试 / 限流 / 重定向 / Cookie

```go
s := lclient.NewSession(
    lclient.WithRetry(lclient.DefaultRetryPolicy()),     // 指数退避 + 抖动，429/5xx 重试
    lclient.WithRateLimit(5, 10),                        // 5 QPS，桶 10
    lclient.WithManualRedirects(true),                   // 由 lclient 跟跳，每跳写 cookie jar
)

// Cookie jar 自动随请求携带 / 回写；可持久化：
s.Jar().SaveToFile("cookies.json")
s.Jar().LoadFromFile("cookies.json")
```

高风控站建议用**有序 header**贴近真实浏览器：

```go
s := lclient.NewSession(lclient.WithDefaultHeaders(lclient.OrderedHeaders{
    {"User-Agent", "..."},
    {"Accept", "..."},
    {"Accept-Language", "en-US,en;q=0.9"},
}))
```

---

## 错误判定

底层错误统一包成 `*RequestError`（可 `errors.Unwrap`），并归类到这些哨兵错误，用
`errors.Is` 判定：

| 哨兵 | 含义 |
|---|---|
| `ErrTimeout` | 请求超时 |
| `ErrNetwork` | DNS / 连接 / TLS 等网络层错误 |
| `ErrCanceled` | 被 context 取消（含在途中止） |
| `ErrTooManyRetries` | 重试耗尽 |
| `ErrBodyTooLarge` | 响应体超过 `WithMaxResponseBytes` |
| `ErrBlockedAddress` | 命中 `WithBlockPrivateIPs` 的内网拦截 |
| `ErrInvalidHeader` | 请求头含 CR/LF 等非法字符 |

---

## 调试 / 指纹自检

```go
s := lclient.NewSession(lclient.WithDebug(true))   // 打印请求/响应（自动脱敏 Cookie/Authorization）

rep, _ := s.CheckFingerprint()                     // 打一次 tls.peet.ws，核对 JA3/JA4/Akamai 指纹
fmt.Printf("%+v\n", rep)
```

---

## 可用 impersonate profile

取值必须是当前链接的 `libcurl-impersonate` 真正编译进去的 target，否则运行时
`curl_easy_impersonate` 会失败。1.2.x 上实测可用的常量见 `impersonate.go`：
`Chrome131/124/120/116/110`、`Edge101/99`、`Safari18(=safari18_0)/17(=safari17_0)`、
`Firefox135/133` 等。换库版本时请重新核对。

---

## 运行示例与测试

- 示例：[`example/lclient`](../example/lclient)
- 单元测试（离线、确定性）：`go test ./lclient/`
- 联网活测（默认跳过）：`LCLIENT_LIVE=1 go test ./lclient/ -run Live`
