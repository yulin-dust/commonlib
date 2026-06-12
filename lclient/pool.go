package lclient

import (
	"sync"

	curl "github.com/yulin-dust/commonlib/third_party/curlimpersonate"
)

// handlePool 是一组可复用 curl 句柄的池子，给 Session 做连接复用。
//
// 为什么需要它：底层每个 curl easy 句柄自带连接缓存 / TLS 会话缓存 / DNS 缓存。
// 复用同一句柄发往同一站点的后续请求，可跳过 TCP + TLS 握手，对爬虫这类高频
// 同站请求是显著的提速。
//
// 并发模型：单个句柄绝不能被两个 goroutine 同时使用。池子通过"借出即从池中移除、
// 归还才放回"来保证任一时刻每个句柄至多被一个 goroutine 持有。
//
//   - get：池中有空闲就取一个，否则新建（允许突发时临时超过 maxIdle）。
//   - put：池没满就放回复用，满了就直接 Close（把稳态空闲句柄数 / 连接数控制在
//     maxIdle 以内）。
type handlePool struct {
	idle    chan *curl.Handle
	maxIdle int

	mu     sync.Mutex
	closed bool
}

// newHandlePool 创建容量为 maxIdle 的池子（至少 1）。
func newHandlePool(maxIdle int) *handlePool {
	if maxIdle <= 0 {
		maxIdle = 1
	}
	return &handlePool{
		idle:    make(chan *curl.Handle, maxIdle),
		maxIdle: maxIdle,
	}
}

// get 借出一个句柄：优先复用空闲句柄，没有则新建。
func (p *handlePool) get() (*curl.Handle, error) {
	select {
	case h := <-p.idle:
		if h != nil {
			return h, nil
		}
	default:
	}
	return curl.NewHandle()
}

// put 归还一个句柄：池未关闭且未满则放回复用，否则直接释放。
func (p *handlePool) put(h *curl.Handle) {
	if h == nil {
		return
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		h.Close()
		return
	}
	select {
	case p.idle <- h:
		// 放回成功，等待下次复用。
	default:
		// 池满：关掉多余句柄，避免空闲连接无上限累积。
		h.Close()
	}
	p.mu.Unlock()
}

// Close 释放池中所有空闲句柄。应在没有在途请求时调用（与 Session.Close 一致）。
// 已借出、尚未归还的句柄会在归还时（put 见 closed）被释放。
func (p *handlePool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()

	for {
		select {
		case h := <-p.idle:
			if h != nil {
				h.Close()
			}
		default:
			return
		}
	}
}
