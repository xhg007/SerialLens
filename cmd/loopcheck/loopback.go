package main

import (
	"errors"
	"sync"
	"time"

	"seriallens/internal/serial"
)

// pipe 内存字节管道：驱动不可用时的降级链路，覆盖 SerialService 的
// 读循环 + 合帧 + hex 事件编码，但不经过 Windows 驱动。
type pipe struct {
	mu     sync.Mutex
	buf    []byte
	closed bool
}

func newPipe() *pipe { return &pipe{} }

func (p *pipe) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

func (p *pipe) close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
}

// read 以 50ms 为界轮询缓冲区：无数据时返回 (0, nil)，
// 与真实实现「阻塞读」在语义上的差别仅在空转粒度。
func (p *pipe) read(b []byte) (int, error) {
	deadline := time.Now().Add(50 * time.Millisecond)
	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return 0, errors.New("loopback closed")
		}
		if len(p.buf) > 0 {
			n := copy(b, p.buf)
			p.buf = p.buf[n:]
			p.mu.Unlock()
			return n, nil
		}
		p.mu.Unlock()
		if time.Now().After(deadline) {
			return 0, nil
		}
		time.Sleep(time.Millisecond)
	}
}

func (p *pipe) write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, errors.New("loopback closed")
	}
	p.buf = append(p.buf, b...)
	return len(b), nil
}

// loopbackPort 把 pipe 适配为 serial.Port；owner 负责关闭整条管道。
type loopbackPort struct {
	p     *pipe
	owner bool
}

func (l *loopbackPort) Open(serial.Config) error { return nil }

func (l *loopbackPort) Close() error {
	if l.owner {
		l.p.close()
	}
	return nil
}

func (l *loopbackPort) Read(b []byte) (int, error)  { return l.p.read(b) }
func (l *loopbackPort) Write(b []byte) (int, error) { return l.p.write(b) }
func (l *loopbackPort) IsOpen() bool                { return !l.p.isClosed() }
