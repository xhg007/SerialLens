package app

import (
	"errors"
	"sync"
	"testing"
	"time"

	"seriallens/internal/events"
	"seriallens/internal/serial"
)

// mockPort 内存实现 serial.Port，供注入测试。
type mockPort struct {
	mu       sync.Mutex
	open     bool
	readCh   chan []byte
	readErr  error
	written  [][]byte
	closeCnt int
}

func newMockPort() *mockPort {
	return &mockPort{readCh: make(chan []byte, 16)}
}

func (m *mockPort) Open(cfg serial.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.open = true
	return nil
}
func (m *mockPort) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeCnt++
	m.open = false
	return nil
}
func (m *mockPort) IsOpen() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.open
}
func (m *mockPort) Read(p []byte) (int, error) {
	if m.readErr != nil {
		return 0, m.readErr
	}
	select {
	case data := <-m.readCh:
		n := copy(p, data)
		return n, nil
	case <-time.After(50 * time.Millisecond):
		return 0, nil
	}
}
func (m *mockPort) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := make([]byte, len(p))
	copy(b, p)
	m.written = append(m.written, b)
	return len(p), nil
}

func newTestSerialService(t *testing.T) (*SerialService, *mockPort, *[]string, *sync.Mutex) {
	port := newMockPort()
	fired := &[]string{}
	mu := &sync.Mutex{}
	emit := func(event string, payload map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		*fired = append(*fired, event)
	}
	return NewSerialService(port, emit), port, fired, mu
}

func connect(t *testing.T, s *SerialService) {
	t.Helper()
	if err := s.Connect(map[string]any{
		"port": "COM3", "baudrate": 115200, "data_bits": 8,
		"stop_bits": 1, "parity": "N", "flow_control": "none",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSerialServiceConnectStatus(t *testing.T) {
	s, _, _, _ := newTestSerialService(t)
	st := s.GetStatus()
	if st["state"] != "disconnected" {
		t.Errorf("initial state = %v", st["state"])
	}
	connect(t, s)
	if s.GetStatus()["state"] != "connected" {
		t.Error("state should be connected")
	}
	if err := s.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if s.GetStatus()["state"] != "disconnected" {
		t.Error("state should be disconnected after Disconnect")
	}
}

func TestSerialServiceBatchesReadData(t *testing.T) {
	s, port, fired, mu := newTestSerialService(t)
	connect(t, s)
	port.readCh <- []byte{0xAA, 0x06}
	port.readCh <- []byte{0x01, 0x02}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		hasData := false
		for _, e := range *fired {
			if e == events.EvSerialData {
				hasData = true
			}
		}
		mu.Unlock()
		if hasData {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, e := range *fired {
		if e == events.EvSerialData {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no EvSerialData among %d events; first=%s", len(*fired), (*fired)[0])
	}
}

func TestSerialServiceDisconnectOnError(t *testing.T) {
	s, port, fired, mu := newTestSerialService(t)
	connect(t, s)
	port.mu.Lock()
	port.readErr = errors.New("device removed")
	port.mu.Unlock()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if s.GetStatus()["state"] == "disconnected" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s.GetStatus()["state"] != "disconnected" {
		t.Fatal("state should become disconnected after read error")
	}
	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, e := range *fired {
		if e == events.EvSerialStatus {
			found = true
		}
	}
	if !found {
		t.Error("EvSerialStatus must be emitted on disconnect")
	}
}

func TestSerialServiceSendHex(t *testing.T) {
	s, port, _, _ := newTestSerialService(t)
	connect(t, s)
	if err := s.Send("48 65", true); err != nil {
		t.Fatal(err)
	}
	port.mu.Lock()
	defer port.mu.Unlock()
	if len(port.written) != 1 || string(port.written[0]) != "\x48\x65" {
		t.Errorf("written = %v", port.written)
	}
	if s.GetStatus()["bytes_out"].(uint64) != 2 {
		t.Errorf("bytes_out = %v", s.GetStatus()["bytes_out"])
	}
}

func TestSerialServiceSendInvalidHex(t *testing.T) {
	s, _, _, _ := newTestSerialService(t)
	connect(t, s)
	if err := s.Send("ZZ", true); err == nil {
		t.Error("invalid hex must error")
	}
	if err := s.Send("", true); err == nil {
		t.Error("empty hex must error")
	}
}

func TestSerialServiceSendTextAsIs(t *testing.T) {
	s, port, _, _ := newTestSerialService(t)
	connect(t, s)
	if err := s.Send("hi", false); err != nil {
		t.Fatal(err)
	}
	port.mu.Lock()
	defer port.mu.Unlock()
	if len(port.written) != 1 || string(port.written[0]) != "hi" {
		t.Errorf("written = %q", port.written)
	}
	if s.GetStatus()["bytes_out"].(uint64) != 2 {
		t.Errorf("bytes_out = %v", s.GetStatus()["bytes_out"])
	}
}
