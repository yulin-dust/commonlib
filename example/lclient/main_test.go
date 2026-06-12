package main

// 这些是面向使用方的集成测试，兼作可运行的用法示例。它们用本地 httptest 服务，
// 完全离线、确定性（除最后一个 LCLIENT_LIVE=1 才跑的联网用例）。
//
//	go test ./example/lclient/
//	LCLIENT_LIVE=1 go test ./example/lclient/ -run Live

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yulin-dust/commonlib/lclient"
)

// 基本 GET：创建会话、发请求、读状态码与正文。
func TestExampleGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello " + r.Header.Get("User-Agent")))
	}))
	defer srv.Close()

	s := lclient.NewSession(lclient.WithImpersonate(lclient.ChromeLatest))
	defer s.Close()

	resp, err := s.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || len(resp.Text()) == 0 {
		t.Fatalf("意外响应: %d %q", resp.StatusCode, resp.Text())
	}
}

// JSON 往返：POST 一个 JSON，服务端回显，客户端再解析回来。
func TestExampleJSONRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"echo": in})
	}))
	defer srv.Close()

	s := lclient.NewSession()
	defer s.Close()

	resp, err := s.Post(srv.URL, lclient.JSON(map[string]any{"name": "测试", "n": 42}))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Echo map[string]any `json:"echo"`
	}
	if err := resp.JSONUnmarshal(&out); err != nil {
		t.Fatal(err)
	}
	if out.Echo["name"] != "测试" {
		t.Fatalf("回显不符: %+v", out.Echo)
	}
}

// 响应体大小闸：超限返回 ErrBodyTooLarge。
func TestExampleMaxResponseBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 64*1024)) // 64 KiB
	}))
	defer srv.Close()

	s := lclient.NewSession(lclient.WithMaxResponseBytes(1024)) // 1 KiB 上限
	defer s.Close()

	_, err := s.Get(srv.URL)
	if !errors.Is(err, lclient.ErrBodyTooLarge) {
		t.Fatalf("期望 ErrBodyTooLarge，得到 %v", err)
	}
}

// SSRF 防护：开启后访问本地（127.0.0.1）被拦截。
func TestExampleBlockPrivateIPs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	s := lclient.NewSession(lclient.WithBlockPrivateIPs(true))
	defer s.Close()

	_, err := s.Get(srv.URL)
	if !errors.Is(err, lclient.ErrBlockedAddress) {
		t.Fatalf("期望 ErrBlockedAddress，得到 %v", err)
	}
}

// 连接池 + 共享缓存 + 并发：演示高频抓取的推荐组合，确保并发安全。
func TestExamplePoolAndSharedCacheConcurrent(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	s := lclient.NewSession(
		lclient.WithConnectionPool(4),
		lclient.WithSharedCache(),
	)
	defer s.Close()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if resp, err := s.Get(srv.URL); err != nil || resp.StatusCode != 200 {
				t.Errorf("并发请求失败: err=%v", err)
			}
		}()
	}
	wg.Wait()
	if hits.Load() != 20 {
		t.Fatalf("期望 20 次命中，实际 %d", hits.Load())
	}
}

// 可中断 context：取消能迅速中止在途请求（而非干等超时）。
func TestExampleContextCancel(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(block)

	s := lclient.NewSession(lclient.WithTimeout(30 * time.Second))
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(150 * time.Millisecond); cancel() }()

	start := time.Now()
	_, err := s.Get(srv.URL, lclient.Ctx(ctx))
	if !errors.Is(err, lclient.ErrCanceled) || time.Since(start) > 5*time.Second {
		t.Fatalf("取消未及时生效: err=%v elapsed=%v", err, time.Since(start))
	}
}

// 联网示例（默认跳过）：身份轮换 + 全部加固，打一个真实站点。
func TestExampleLive(t *testing.T) {
	if os.Getenv("LCLIENT_LIVE") != "1" {
		t.Skip("set LCLIENT_LIVE=1 to run")
	}
	s := lclient.NewSession(
		lclient.WithRandomIdentity(lclient.WithRandomAcceptLanguage()),
		lclient.WithConnectionPool(4),
		lclient.WithSharedCache(),
		lclient.WithBlockPrivateIPs(true),
		lclient.WithMaxResponseBytes(10<<20),
	)
	defer s.Close()

	resp, err := s.Get("https://postman-echo.com/get")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	t.Logf("ok: %d bytes in %s", len(resp.Body), resp.Elapsed.Round(time.Millisecond))
}
