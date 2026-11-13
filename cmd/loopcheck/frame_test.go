package main

import (
	"bytes"
	"testing"
	"time"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []uint32{0, 1, 2, 1000, 1<<31 - 1, 1 << 31, ^uint32(0)}
	for _, seq := range cases {
		buf := make([]byte, FrameSize)
		if !EncodeFrame(buf, seq) {
			t.Fatalf("seq %d: encode failed", seq)
		}
		f, ok := DecodeFrame(buf)
		if !ok {
			t.Fatalf("seq %d: decode failed", seq)
		}
		if f.Seq != seq {
			t.Errorf("seq mismatch: got %d want %d", f.Seq, seq)
		}
		if !VerifyFrame(f) {
			t.Errorf("seq %d: payload verify failed", seq)
		}
	}
}

func TestEncodeRejectsShortBuffer(t *testing.T) {
	if EncodeFrame(make([]byte, FrameSize-1), 1) {
		t.Error("must reject short buffer")
	}
}

func TestVerifyDetectsCorruption(t *testing.T) {
	buf := make([]byte, FrameSize)
	EncodeFrame(buf, 42)
	buf[10] ^= 0xFF
	f, _ := DecodeFrame(buf)
	if VerifyFrame(f) {
		t.Error("corrupted payload must not verify")
	}
}

func TestCollectorCrossEventSplicing(t *testing.T) {
	// 把 3 帧按 17 字节一段切碎喂入，模拟事件边界与帧边界不对齐
	var stream bytes.Buffer
	for seq := uint32(0); seq < 3; seq++ {
		b := make([]byte, FrameSize)
		EncodeFrame(b, seq)
		stream.Write(b)
	}
	raw := stream.Bytes()
	c := &Collector{}
	now := time.Now()
	for i := 0; i < len(raw); i += 17 {
		end := i + 17
		if end > len(raw) {
			end = len(raw)
		}
		c.Feed(raw[i:end], now)
		now = now.Add(33 * time.Millisecond)
	}
	r := c.Report(len(raw), time.Second)
	if !r.Pass() {
		t.Fatalf("report = %+v", r)
	}
	if r.FramesOK != 3 {
		t.Errorf("framesOK = %d, want 3", r.FramesOK)
	}
}

func TestCollectorDetectsMissing(t *testing.T) {
	// 序列 0,1,2,5：应记 missing=2（3 与 4 缺失）
	var stream bytes.Buffer
	for _, seq := range []uint32{0, 1, 2, 5} {
		b := make([]byte, FrameSize)
		EncodeFrame(b, seq)
		stream.Write(b)
	}
	c := &Collector{}
	c.Feed(stream.Bytes(), time.Now())
	r := c.Report(stream.Len(), time.Second)
	if r.Missing != 2 {
		t.Errorf("missing = %d, want 2", r.Missing)
	}
	if r.Pass() {
		t.Error("must not pass with missing frames")
	}
}

func TestCollectorDetectsDuplicate(t *testing.T) {
	var stream bytes.Buffer
	for _, seq := range []uint32{0, 1, 1, 2} {
		b := make([]byte, FrameSize)
		EncodeFrame(b, seq)
		stream.Write(b)
	}
	c := &Collector{}
	c.Feed(stream.Bytes(), time.Now())
	r := c.Report(stream.Len(), time.Second)
	if r.Dup != 1 {
		t.Errorf("dup = %d, want 1", r.Dup)
	}
	if r.Pass() {
		t.Error("must not pass with duplicate frames")
	}
}

func TestCollectorDetectsCorrupt(t *testing.T) {
	b := make([]byte, FrameSize)
	EncodeFrame(b, 7)
	b[20] ^= 0x01
	c := &Collector{}
	c.Feed(b, time.Now())
	r := c.Report(len(b), time.Second)
	if r.Corrupt != 1 {
		t.Errorf("corrupt = %d, want 1", r.Corrupt)
	}
	if r.Pass() {
		t.Error("must not pass with corrupt frame")
	}
}

func TestCollectorTracksEventGap(t *testing.T) {
	b := make([]byte, FrameSize)
	EncodeFrame(b, 0)
	c := &Collector{}
	t0 := time.Now()
	c.Feed(b[:32], t0)
	c.Feed(b[32:], t0.Add(120*time.Millisecond))
	if got := c.Report(FrameSize, time.Second).MaxEventGapMs; got < 100 {
		t.Errorf("maxEventGap = %dms, want >= 100", got)
	}
}

func TestCollectorIncompleteFrameWaits(t *testing.T) {
	b := make([]byte, FrameSize)
	EncodeFrame(b, 0)
	c := &Collector{}
	c.Feed(b[:FrameSize-1], time.Now())
	if r := c.Report(FrameSize-1, time.Second); r.FramesOK != 0 {
		t.Errorf("framesOK = %d, want 0 before frame completes", r.FramesOK)
	}
	c.Feed(b[FrameSize-1:], time.Now())
	if r := c.Report(FrameSize, time.Second); r.FramesOK != 1 {
		t.Errorf("framesOK = %d, want 1 after completion", r.FramesOK)
	}
}
