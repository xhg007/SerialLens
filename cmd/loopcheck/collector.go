package main

import (
	"sync"
	"time"
)

// Collector 把到达的字节流切片成帧并统计缺失/重复/内容错配。
// 事件可能把一帧拆到两次推送里（合帧边界与帧边界无关），
// 因此必须先拼接再解析 —— 这正是「跨事件拼接」的验收入口。
// 并发约定：Feed 在串口读 goroutine 调用，Report/Received 在主 goroutine 调用，故加锁。
type Collector struct {
	mu       sync.Mutex
	buf      []byte
	expected uint32
	started  bool

	framesOK  int
	missing   int
	dup       int
	corrupt   int
	received  int
	eventCnt  int
	lastEvent time.Time
	maxGap    time.Duration
}

// Received 返回已收到的字节数（并发安全）。
func (c *Collector) Received() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.received
}

// BadBytes 返回解析失败的字节数（hex 解码失败等）。
func (c *Collector) BadBytes(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.corrupt += n
}

// Feed 追加一段字节流并解析出完整的帧。
// now 由调用方传入事件到达时间，便于统计事件间隔。
func (c *Collector) Feed(b []byte, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.received += len(b)
	c.eventCnt++
	if !c.lastEvent.IsZero() {
		if gap := now.Sub(c.lastEvent); gap > c.maxGap {
			c.maxGap = gap
		}
	}
	c.lastEvent = now

	c.buf = append(c.buf, b...)
	for len(c.buf) >= FrameSize {
		f, ok := DecodeFrame(c.buf[:FrameSize])
		if !ok {
			break
		}
		c.buf = c.buf[FrameSize:]
		c.accept(f)
	}
}

func (c *Collector) accept(f Frame) {
	if !VerifyFrame(f) {
		c.corrupt++
		// 内容错配说明流已失步，重置期望值以免后续全部误判为乱序
		c.expected = f.Seq + 1
		c.started = true
		return
	}
	if !c.started {
		c.started = true
		c.expected = f.Seq
	}
	switch {
	case f.Seq == c.expected:
		c.framesOK++
		c.expected++
	case f.Seq > c.expected:
		gap := int(f.Seq - c.expected)
		c.missing += gap
		c.framesOK++
		c.expected = f.Seq + 1
	default: // f.Seq < expected
		c.dup++
	}
}

// Report 汇总一次验收的结论，字段与 docs/acceptance/m0-m1.md 表格一一对应。
type Report struct {
	Sent           int
	Received       int
	Events         int
	FramesExpected int
	FramesOK       int
	Missing        int
	Dup            int
	Corrupt        int
	MaxEventGapMs  int64
	Elapsed        time.Duration
	ThroughputBps  float64
}

// Report 由发送字节数与采集器状态生成报告。
func (c *Collector) Report(sent int, elapsed time.Duration) Report {
	c.mu.Lock()
	defer c.mu.Unlock()
	secs := elapsed.Seconds()
	var bps float64
	if secs > 0 {
		bps = float64(c.received) / secs
	}
	expectedFrames := sent / FrameSize
	if c.started {
		expectedFrames = int(c.expected)
	}
	return Report{
		Sent:           sent,
		Received:       c.received,
		Events:         c.eventCnt,
		FramesExpected: expectedFrames,
		FramesOK:       c.framesOK,
		Missing:        c.missing,
		Dup:            c.dup,
		Corrupt:        c.corrupt,
		MaxEventGapMs:  c.maxGap.Milliseconds(),
		Elapsed:        elapsed,
		ThroughputBps:  bps,
	}
}

// Pass 判定：零缺失、零重复、零错配且收到全部字节。
func (r Report) Pass() bool {
	return r.Missing == 0 && r.Dup == 0 && r.Corrupt == 0 && r.Received == r.Sent
}
