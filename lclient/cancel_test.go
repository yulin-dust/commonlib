package lclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ctx 取消应能中止"在途"请求并迅速返回（而非干等到 curl 超时）。
// 用一个会挂起很久的本地服务来制造在途等待。
func TestContextCancelInterruptsInflight(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 一直挂起，直到测试结束或客户端断开。
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(block)

	// 较长的请求超时（30s），以证明返回是"取消"而非"超时"。
	s := NewSession(WithTimeout(30 * time.Second))
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	// 200ms 后取消。
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := s.Get(srv.URL, Ctx(ctx))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("期望被取消，却成功了")
	}
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("期望 ErrCanceled，得到 %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("取消未及时中止在途请求，耗时 %v（不应接近 30s 超时）", elapsed)
	}
	t.Logf("取消及时生效：%v 后返回 %v", elapsed.Round(time.Millisecond), err)
}

// 超时 context 同样应中止在途请求。
func TestContextTimeoutInterruptsInflight(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(block)

	s := NewSession(WithTimeout(30 * time.Second))
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := s.Get(srv.URL, Ctx(ctx))
	elapsed := time.Since(start)

	if err == nil || elapsed > 5*time.Second {
		t.Fatalf("超时未及时中止：err=%v elapsed=%v", err, elapsed)
	}
	t.Logf("超时及时生效：%v 后返回", elapsed.Round(time.Millisecond))
}
