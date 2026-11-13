// Package framer 把串口字节流切成「协议帧」（PRD §F2）。
//
// 两种切帧方式：
//  1. 默认 IDLE 切帧：距最后一次收到数据超过 idle（默认 20ms）即认为一帧结束；
//  2. 脚本 detect_frame 覆盖：脚本返回边界长度，返回 0 表示「数据不足，继续等」。
//
// 超长保护：缓冲区超过 MaxFrame 时强制切出一帧，避免脚本 bug 或垃圾数据把内存吃满。
// 非并发安全：由单一消费 goroutine 持有（ParserService.consumeLoop）。
package framer

import (
	"errors"
	"time"
)

// ErrBadBoundary 脚本 detect_frame 返回了非法边界（负数或超过单帧上限）。
var ErrBadBoundary = errors.New("framer: detect_frame returned invalid boundary")

// DefaultIdle 默认 IDLE 阈值（PRD §F2）。
const DefaultIdle = 20 * time.Millisecond

// DefaultMaxFrame 单帧上限，超长强制切分。
const DefaultMaxFrame = 4096

// DetectFunc 脚本自定义边界：返回应切出的字节数，0 表示继续等待。
type DetectFunc func(buf []byte) (int, error)

// Framer 增量切帧器。
type Framer struct {
	idle     time.Duration
	maxFrame int
	detect   DetectFunc

	buf      []byte
	lastFeed time.Time
}

// New 创建切帧器；detect 可为 nil（纯 IDLE 模式）。
func New(idle time.Duration, maxFrame int, detect DetectFunc) *Framer {
	if idle <= 0 {
		idle = DefaultIdle
	}
	if maxFrame <= 0 {
		maxFrame = DefaultMaxFrame
	}
	return &Framer{idle: idle, maxFrame: maxFrame, detect: detect}
}

// SetDetect 热更新自定义边界（脚本重载时调用）。
func (f *Framer) SetDetect(detect DetectFunc) {
	f.detect = detect
}

// Feed 追加数据并返回本次可确定的帧。
// 注意：IDLE 模式下 Feed 只能产出「超长强制帧」，正常帧要等 FlushIdle。
func (f *Framer) Feed(data []byte, now time.Time) ([][]byte, error) {
	if len(data) > 0 {
		f.buf = append(f.buf, data...)
		f.lastFeed = now
	}
	if f.detect == nil {
		return f.drainOversize(), nil
	}
	return f.drainByDetect()
}

// drainOversize 无脚本边界时的超长保护。
func (f *Framer) drainOversize() [][]byte {
	var out [][]byte
	for len(f.buf) >= f.maxFrame {
		frame := make([]byte, f.maxFrame)
		copy(frame, f.buf[:f.maxFrame])
		out = append(out, frame)
		f.buf = append(f.buf[:0], f.buf[f.maxFrame:]...)
	}
	return out
}

// drainByDetect 反复询问脚本边界并切帧。
func (f *Framer) drainByDetect() ([][]byte, error) {
	var out [][]byte
	for len(f.buf) > 0 {
		n, err := f.detect(f.buf)
		if err != nil {
			// 脚本报错：丢弃缓冲，避免错误数据无限堆积
			f.buf = f.buf[:0]
			return out, err
		}
		if n == 0 {
			// 数据不足；但已达上限说明脚本边界逻辑失效，强制切一帧避免死等
			if len(f.buf) >= f.maxFrame {
				out = append(out, f.take(f.maxFrame))
				continue
			}
			return out, nil
		}
		if n < 0 || n > f.maxFrame {
			f.buf = f.buf[:0]
			return out, ErrBadBoundary
		}
		if n > len(f.buf) {
			// 脚本要求超过现有数据：等更多数据（异常大时由上面的上限兜底）
			if len(f.buf) >= f.maxFrame {
				out = append(out, f.take(f.maxFrame))
				continue
			}
			return out, nil
		}
		out = append(out, f.take(n))
	}
	return out, nil
}

func (f *Framer) take(n int) []byte {
	frame := make([]byte, n)
	copy(frame, f.buf[:n])
	f.buf = append(f.buf[:0], f.buf[n:]...)
	return frame
}

// FlushIdle 由定时器调用：距最后收数据已超过 idle 则把缓冲作为一帧切出。
func (f *Framer) FlushIdle(now time.Time) [][]byte {
	if len(f.buf) == 0 {
		return nil
	}
	if now.Sub(f.lastFeed) < f.idle {
		return nil
	}
	frame := make([]byte, len(f.buf))
	copy(frame, f.buf)
	f.buf = f.buf[:0]
	return [][]byte{frame}
}

// Pending 缓冲中未成帧的字节数（诊断用）。
func (f *Framer) Pending() int { return len(f.buf) }

// Reset 清空状态（工程切换/断线）。
func (f *Framer) Reset() {
	f.buf = f.buf[:0]
	f.lastFeed = time.Time{}
}
