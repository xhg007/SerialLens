package app

import (
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"seriallens/internal/events"
	"seriallens/internal/serial"
)

// blockingPort 模拟真实驱动语义：只交付一次数据，之后 Read 永久阻塞，
// 直到 Close 才返回错误（go.bug.st 的阻塞读行为）。
type blockingPort struct {
	mu        sync.Mutex
	open      bool
	data      []byte
	delivered bool
	unblock   chan struct{}
}

func newBlockingPort(data []byte) *blockingPort {
	return &blockingPort{data: data, unblock: make(chan struct{})}
}

func (b *blockingPort) Open(serial.Config) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.open = true
	return nil
}

func (b *blockingPort) Close() error {
	b.mu.Lock()
	b.open = false
	b.mu.Unlock()
	select {
	case <-b.unblock:
	default:
		close(b.unblock)
	}
	return nil
}

func (b *blockingPort) Read(p []byte) (int, error) {
	b.mu.Lock()
	if !b.delivered {
		b.delivered = true
		n := copy(p, b.data)
		b.mu.Unlock()
		return n, nil
	}
	b.mu.Unlock()
	<-b.unblock
	return 0, errors.New("closed")
}

func (b *blockingPort) Write(p []byte) (int, error) { return len(p), nil }

func (b *blockingPort) IsOpen() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.open
}

// 设备发完最后一帧就安静时，尾部数据必须在一个合帧周期内推送，
// 不能因为 readLoop 阻塞在 Read 里而无限滞留。
func TestSerialServiceFlushesTailWhenIdle(t *testing.T) {
	port := newBlockingPort([]byte{0xAA, 0xBB, 0xCC})
	var mu sync.Mutex
	var fired []string
	svc := NewSerialService(port, func(event string, payload map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		fired = append(fired, event)
	})
	if err := svc.Connect(map[string]any{"port": "X", "baudrate": 115200}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = svc.Disconnect() }()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		for _, e := range fired {
			if e == events.EvSerialData {
				mu.Unlock()
				return
			}
		}
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("tail data stranded: no serial:data within 500ms")
}

// C1: 重连后 Disconnect 必须仍然生效（stopOnce 跨连接复用的回归测试）。
func TestSerialServiceReconnectDisconnects(t *testing.T) {
	s, port, _, _ := newTestSerialService(t)
	for round := 1; round <= 2; round++ {
		connect(t, s)
		if s.GetStatus()["state"] != "connected" {
			t.Fatalf("round %d: not connected", round)
		}
		if err := s.Disconnect(); err != nil {
			t.Fatalf("round %d: disconnect: %v", round, err)
		}
		if s.GetStatus()["state"] != "disconnected" {
			t.Fatalf("round %d: state should be disconnected", round)
		}
	}
	port.mu.Lock()
	cnt := port.closeCnt
	port.mu.Unlock()
	if cnt != 2 {
		t.Errorf("port closed %d times, want 2", cnt)
	}
}

// C1/I1: 反复连接断开不得泄漏读 goroutine。
func TestSerialServiceNoGoroutineLeak(t *testing.T) {
	s, _, _, _ := newTestSerialService(t)
	baseline := runtime.NumGoroutine()
	for i := 0; i < 10; i++ {
		connect(t, s)
		if err := s.Disconnect(); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= baseline+1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("goroutines: baseline=%d now=%d (leak)", baseline, runtime.NumGoroutine())
}

// I1: Disconnect 必须等 readLoop 真正退出后再返回。
func TestSerialServiceDisconnectWaitsReadLoop(t *testing.T) {
	s, port, _, _ := newTestSerialService(t)
	connect(t, s)
	port.readCh <- []byte{0x01}
	time.Sleep(60 * time.Millisecond)
	if err := s.Disconnect(); err != nil {
		t.Fatal(err)
	}
	// Disconnect 返回后读循环必须已退出：再次 Connect 不会有两个读循环
	if s.reading {
		t.Error("readLoop still running after Disconnect")
	}
}

// 流式接收期间 RX 计数必须刷新：serial:status 至少每秒推一次，
// 否则前端 bytes_in 只在连接/断开时更新。
func TestSerialServiceEmitsStatusWhileStreaming(t *testing.T) {
	port := newMockPort()
	var mu sync.Mutex
	statuses := []map[string]any{}
	svc := NewSerialService(port, func(event string, payload map[string]any) {
		if event != events.EvSerialStatus {
			return
		}
		mu.Lock()
		statuses = append(statuses, payload)
		mu.Unlock()
	})
	if err := svc.Connect(map[string]any{"port": "X", "baudrate": 115200}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = svc.Disconnect() }()

	port.readCh <- []byte{1, 2, 3, 4, 5}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		for _, p := range statuses {
			if in, ok := p["bytes_in"].(uint64); ok && in > 0 {
				mu.Unlock()
				return
			}
		}
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("no serial:status with bytes_in>0 within 2s while streaming")
}

// M3: 未连接时 Send 返回明确错误。
func TestSerialServiceSendWhenDisconnected(t *testing.T) {
	s, _, _, _ := newTestSerialService(t)
	if err := s.Send("hi", false); err == nil {
		t.Error("send while disconnected must error")
	}
}
