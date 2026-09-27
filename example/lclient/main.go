// Command lclient-example 演示 lclient 的主要能力：浏览器指纹模拟 + 身份轮换 +
// 连接池 + 跨句柄共享缓存 + SSRF 防护 + 响应体闸 + 可中断 context。
//
// 运行（不传参时抓一个 GB2312 页面，顺带演示自动转码；也可传入任意 URL）：
//
//	go run ./example/lclient
//	go run ./example/lclient https://httpbin.org/get
//
// 想改为自检 TLS 指纹，用 s.CheckFingerprint()（会打 tls.peet.ws）。
//
// 注意：本包是 cgo，需先按 lclient/README 装好原生库 libcurl-impersonate。
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/yulin-dust/commonlib/lclient"
)

func main() {
	// 默认目标是个 GB2312 编码的页面，用来顺带演示 WithAutoDecode 的效果。
	url := "https://www.chinanews.com.cn/edu/2013/06-08/4911353.shtml"
	if len(os.Args) > 1 {
		url = os.Args[1]
	}

	// 一个"全副武装"的会话：把本仓库加固过的能力都打开。
	s := lclient.NewSession(
		lclient.WithRandomIdentity(lclient.WithRandomAcceptLanguage()), // 每请求随机一套自洽浏览器身份
		lclient.WithConnectionPool(8),                                  // 复用连接，省握手
		lclient.WithSharedCache(),                                      // 跨句柄共享 DNS / TLS 会话缓存
		lclient.WithBlockPrivateIPs(true),                              // SSRF 防护：拦截内网/环回
		lclient.WithMaxResponseBytes(10<<20),                           // 响应体上限 10 MiB
		lclient.WithRetry(lclient.DefaultRetryPolicy()),                // 429/5xx 指数退避重试
		lclient.WithAutoDecode(true),                                   // 自动把 GB2312/GBK/Big5 等转 UTF-8，避免乱码
	)
	defer s.Close()

	// 给整次请求一个超时 context——取消能中止在途传输，不必干等 curl 超时。
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := s.Get(url, lclient.Ctx(ctx))
	if err != nil {
		log.Fatalf("请求失败: %v", err)
	}

	fmt.Printf("GET %s\n  -> %d  (%d bytes, %s)\n", url, resp.StatusCode, len(resp.Body), resp.Elapsed.Round(time.Millisecond))
	if resp.FinalURL != url {
		fmt.Printf("  重定向落点: %s\n", resp.FinalURL)
	}
	body := resp.Text()
	if len(body) > 2000 {
		body = body[:2000] + "..."
	}
	fmt.Println("---- body (truncated) ----")
	fmt.Println(body)
}
