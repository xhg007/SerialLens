package app

import (
	"runtime"
	"testing"
	"time"
)

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

// M3: 未连接时 Send 返回明确错误。
func TestSerialServiceSendWhenDisconnected(t *testing.T) {
	s, _, _, _ := newTestSerialService(t)
	if err := s.Send("hi", false); err == nil {
		t.Error("send while disconnected must error")
	}
}
