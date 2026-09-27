# lclient

基于 [`go-curl-impersonate`](https://github.com/TeamMilestone/go-curl-impersonate)（底层
[`libcurl-impersonate`](https://github.com/lexiforest/curl-impersonate)）的 Go HTTP 客户端，
提供 **Python `requests` 风格** 的 API + **完整浏览器 TLS / HTTP2 指纹模拟**，并在此基础上做了
一层面向爬虫的工程加固：浏览器身份轮换、连接复用、跨句柄共享缓存、SSRF 防护、响应体闸、
真正可中断的 context 取消等。

---

## 这个库是做什么的

一句话：**让 Go 发出的 HTTP 请求在网络层面看起来就是一个真实浏览器**，并且把爬虫工程里
反复要造的那些轮子（身份轮换、连接复用、限流重试、SSRF 防护、字符集解码）一次配齐。

标准库 `net/http` 的 TLS ClientHello 和 HTTP/2 SETTINGS 帧有一套 Go 独有的特征。风控系统
（Cloudflare、Akamai、DataDome、PerimeterX、阿里 / 腾讯系）在**握手阶段**就能算出 JA3 / JA4 /
Akamai 指纹，此时连一个字节的 HTTP 请求都还没发出去——换 User-Agent、加 header、挂代理，
全部无效，因为被识别的不是这些。

lclient 把请求交给 `libcurl-impersonate`（curl 的一个 fork，逐字节复刻了各版本 Chrome /
Firefox / Safari 的 TLS 扩展顺序、密码套件、椭圆曲线、ALPS、HTTP/2 SETTINGS 与 header
优先级），因此指纹是**真的**对得上，而不是"看起来像"。在此之上包了一层 Python `requests`
风格的 API 和一批面向抓取的加固。

典型场景：抓电商 / 新闻 / 社区站点的公开页面，做数据采集、价格监控、内容聚合，且目标站
已经上了 JA3 / JA4 级别的风控。

### 它不做什么

- **不执行 JavaScript**。目标站的内容靠 JS 渲染，或风控要求跑 JS 挑战（Cloudflare 的
  Turnstile / "Checking your browser" 页面），本库帮不上——那需要真浏览器（chromedp /
  Playwright）或打码服务。lclient 解决的是**握手层**识别，不是**行为层**挑战。
- **不管账号、验证码、代理池调度**。代理要你自己传，IP 质量本库管不了。
- **不是通用 HTTP 客户端**。内部请求、微服务调用请继续用 `net/http`——那些场景不需要
  指纹伪装，却要付出下面"缺点"里的全部代价。

---

## 优点 / 缺点

### 优点

| | |
|---|---|
| **指纹是真的** | JA3 / JA4 / Akamai 指纹逐字节对齐真实浏览器，可用 `CheckFingerprint()` 自验 |
| **整套身份轮换** | TLS profile + UA + `sec-ch-ua` + header 顺序**成套**切换、内部自洽，不会出现"UA 说是 Chrome、指纹却是 Firefox"这种一眼假 |
| **API 顺手** | `requests` 风格，`s.Get(url, Params{...}, Headers{...}, Timeout(...))`，不用手搓 `http.NewRequest` |
| **爬虫加固齐全** | 连接池 / 共享 DNS+TLS 缓存 / 限流 / 退避重试 / 响应体闸 / SSRF 防护 / 可中断 context，开箱即用 |
| **默认值安全** | 默认校验 TLS 证书、只允许 http(s)、拒绝 CRLF 注入的 header、自动转 UTF-8 |
| **有序 header** | header 顺序本身就是指纹，`OrderedHeaders` 让你精确控制 |

### 缺点与限制

| | |
|---|---|
| **cgo，装起来麻烦** | 必须先装原生 `libcurl-impersonate`。交叉编译困难，Docker 镜像变大，CI 要额外装依赖，`CGO_ENABLED=0` 直接编不过 |
| **不支持 Windows** | 只测过 macOS / Linux |
| **无流式响应** | 响应体一次性读进内存（`[]byte`），没有 `io.Reader`。抓大文件请用 `WithMaxResponseBytes` 设闸，或换别的客户端 |
| **每请求一次 cgo 调用** | 调用期间占住一个 OS 线程。极高并发下线程数会涨，比纯 Go 客户端吃资源 |
| **profile 跟库版本绑死** | `Chrome131` 这些常量必须是当前链接的 libcurl-impersonate 真编译进去的 target，换库版本要重新核对；浏览器版本更新后指纹会逐渐过时，得跟着升级底层库 |
| **没有 HTTP/3** | 底层未启用 QUIC |
| **身份轮换会降低连接复用率** | 不同身份 TLS 参数不同，curl 不复用为另一身份建的连接。要最大化复用就固定单一身份 |
| **SSRF 防护只对直连有效** | 走代理时连接的是代理，无法按连接 IP 判定目标，会自动跳过 |
| **cookie jar 是简化实现** | 不带公共后缀列表（PSL），只靠"Domain 必须含点且覆盖当前 host"兜底；没有 `SameSite` 语义 |
| **跨域重定向的 cookie 归属不精确** | curl 自动跟随时，各跳的 `Set-Cookie` 统一按最初的 URL 入库。跨站跳转需要精确归属时请开 `WithManualRedirects` |

**一句话取舍**：目标站有 JA3 / JA4 级别的风控，就值得吃下 cgo 这份麻烦；否则用
`net/http` 更省事。

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

> **依赖说明**：lclient 依赖的 `go-curl-impersonate` 是一个 fork，已经作为普通包放在
> 本 module 内（`commonlib/third_party/curlimpersonate`），**不需要任何 `replace`**。
> `go get github.com/yulin-dust/commonlib` 之后直接 import `lclient` 即可，Go 会把
> 那个包一并拉下来。要装的只有原生库 `libcurl-impersonate`。

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
    lclient.Timeout(10*time.Second),  // 毫秒精度，亚秒超时也有效
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

**重定向语义**：

- `WithMaxRedirects(n)` 在两种模式下都生效（默认 10 跳）。
- `POST` 遇到 301 / 302 / 303 会按浏览器与 RFC 9110 的行为**降级为 GET 并丢弃 body**；
  307 / 308 保持方法与 body 不变。
- `PUT` / `PATCH` / `DELETE` 在**自动跟随**模式下会被 libcurl 带到每一跳（它对自定义方法
  的既定行为）。需要严格的降级语义时请用 `WithManualRedirects(true)`。
- 响应 header 取的是**最终一跳**：`resp.HeaderGet("Content-Type")` 不会拿到中间 302 页面的
  值。唯独 `Set-Cookie` 跨所有跳累积，中间跳下发的登录态不会丢。

**拿最终 URL**（解析页面里的相对链接必须用它）：

```go
resp, _ := s.Get("https://example.com/a")   // 302 → /sub/b
resp.URL       // "https://example.com/a"       最初发起的地址
resp.FinalURL  // "https://example.com/sub/b"   跟完重定向的落点
```

**Cookie 作用域规则**（贴近 RFC 6265）：

- 不带 `Domain` 属性的 cookie 是 **host-only**，只发回下发它的那个 host，不外溢到子域；
  带 `Domain=example.com` 的才会发给 `a.example.com`。
- 路径匹配在 `/` 边界上，`Path=/ab` 的 cookie 不会被发给 `/abc`。
- 服务器声明的 `Domain` 必须覆盖当前 host 且不能是单标签公共后缀，否则丢弃
  （挡 `evil.com` 给 `bank.com` / 顶级域写 cookie）。

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

开启 `WithRetry` 后这些判定**依然成立**：重试耗尽的错误同时包住 `ErrTooManyRetries`
与底层原因，`errors.Is(err, ErrTimeout)` 和 `errors.Is(err, ErrTooManyRetries)` 都为真。

确定性失败不会被重试——`ErrCanceled` / `ErrBodyTooLarge` / `ErrBlockedAddress` /
`ErrInvalidHeader` 重试同样的请求只会得到同样的结果，直接返回，不浪费退避时间、也不
无谓加压目标站。

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
