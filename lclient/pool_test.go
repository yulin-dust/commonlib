package lclient

import (
	"sync"
	"testing"
)

// 归还后再借出应拿到同一个句柄（确实复用，而非每次新建）。
func TestHandlePoolReuse(t *testing.T) {
	p := newHandlePool(2)
	defer p.Close()
	h1, err := p.get()
	if err != nil {
		t.Fatal(err)
	}
	p.put(h1)
	h2, err := p.get()
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Error("归还后再借出应复用同一句柄")
	}
	p.put(h2)
}

// 池空时借出应新建不同句柄；超出容量的归还应被释放。
func TestHandlePoolCapacity(t *testing.T) {
	p := newHandlePool(1)
	h1, _ := p.get()
	h2, _ := p.get() // 池空 → 新建
	if h1 == h2 {
		t.Fatal("池空时应新建不同句柄")
	}
	p.put(h1) // 放回（容量 1）
	p.put(h2) // 池满 → 直接关闭，不泄漏
	p.Close()
}

// 关闭后归还不应 panic；借出仍可新建。
func TestHandlePoolClosed(t *testing.T) {
	p := newHandlePool(2)
	h, _ := p.get()
	p.Close()
	p.put(h) // 应直接 Close，不 panic
	h2, err := p.get()
	if err != nil {
		t.Fatal(err)
	}
	h2.Close()
}

// 高并发借还：单个句柄不会被并发持有；配合 -race 检测数据竞争。
func TestHandlePoolConcurrent(t *testing.T) {
	p := newHandlePool(4)
	defer p.Close()
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 15; j++ {
				h, err := p.get()
				if err != nil {
					t.Error(err)
					return
				}
				p.put(h)
			}
		}()
	}
	wg.Wait()
}
