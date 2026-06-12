package main

// 用 lclient 抓取页面、用 go-trafilatura 解析，打印解析结果。
//   go test ./ -run TestTrafilaturaViaLclient -v

import (
	"bytes"
	"net/url"
	"strings"
	"testing"

	"github.com/markusmobius/go-trafilatura"
	"github.com/yulin-dust/commonlib/lclient"
	"golang.org/x/net/html"
)

func TestTrafilaturaViaLclient(t *testing.T) {
	const target = "https://www.9game.cn/xinghezhanji1/11898131.html"

	// 1) 用 lclient 抓取（随机浏览器身份 + 连接池）。
	s := lclient.NewSession(
		lclient.WithRandomIdentity(lclient.WithRandomAcceptLanguage()),
		lclient.WithConnectionPool(2),
	)
	defer s.Close()

	resp, err := s.Get(target)
	if err != nil {
		t.Fatalf("lclient 抓取失败: %v", err)
	}
	t.Logf("HTTP %d, %d bytes", resp.StatusCode, len(resp.Body))
	if resp.StatusCode != 200 {
		t.Fatalf("非 200 响应: %d", resp.StatusCode)
	}

	// 2) 交给 go-trafilatura 解析（开启 fallback：readability + domdistiller 兜底）。
	u, _ := url.Parse(target)
	res, err := trafilatura.Extract(bytes.NewReader(resp.Body), trafilatura.Options{
		OriginalURL:    u,
		EnableFallback: true,
		IncludeImages:  true,
		IncludeLinks:   true,
	})
	if err != nil {
		t.Fatalf("trafilatura 解析失败: %v", err)
	}

	// 3) 打印结果。
	m := res.Metadata
	t.Log("==================== Metadata ====================")
	t.Logf("Title:       %s", m.Title)
	t.Logf("Author:      %s", m.Author)
	t.Logf("URL:         %s", m.URL)
	t.Logf("Hostname:    %s", m.Hostname)
	t.Logf("Sitename:    %s", m.Sitename)
	if !m.Date.IsZero() {
		t.Logf("Date:        %s", m.Date.Format("2006-01-02 15:04:05"))
	}
	t.Logf("Description: %s", m.Description)
	t.Logf("Categories:  %v", m.Categories)
	t.Logf("Tags:        %v", m.Tags)
	t.Logf("License:     %s", m.License)
	t.Logf("Language:    %s", m.Language)
	t.Logf("Image:       %s", m.Image)
	t.Logf("PageType:    %s", m.PageType)

	t.Log("==================== ContentText（纯文本）====================")
	t.Logf("\n%s", res.ContentText)

	if res.ContentNode != nil {
		var buf bytes.Buffer
		if err := html.Render(&buf, res.ContentNode); err == nil {
			h := buf.String()
			if len(h) > 6000 {
				h = h[:6000] + "\n...(截断)"
			}
			t.Log("==================== ContentNode（正文 HTML）====================")
			t.Logf("\n%s", h)
		}
	}

	if strings.TrimSpace(res.CommentsText) != "" {
		t.Log("==================== CommentsText（评论）====================")
		t.Logf("\n%s", res.CommentsText)
	}
}
