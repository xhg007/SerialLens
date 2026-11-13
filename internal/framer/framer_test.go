package framer

import (
	"errors"
	"testing"
	"time"
)

func TestIdleSplitsOnGap(t *testing.T) {
	f := New(20*time.Millisecond, 0, nil)
	base := time.Now()

	frames, err := f.Feed([]byte{1, 2}, base)
	if err != nil || len(frames) != 0 {
		t.Fatalf("feed1 = %v, %v", frames, err)
	}
	// 未超时：不切
	if got := f.FlushIdle(base.Add(10 * time.Millisecond)); len(got) != 0 {
		t.Fatalf("premature flush = %v", got)
	}
	// 超时：整段一帧
	got := f.FlushIdle(base.Add(25 * time.Millisecond))
	if len(got) != 1 || len(got[0]) != 2 || got[0][0] != 1 {
		t.Fatalf("flush = %v", got)
	}
	// 再 flush 无数据
	if got := f.FlushIdle(base.Add(50 * time.Millisecond)); len(got) != 0 {
		t.Fatalf("second flush = %v", got)
	}
}

func TestIdleMergesBackToBackChunks(t *testing.T) {
	f := New(20*time.Millisecond, 0, nil)
	base := time.Now()
	_, _ = f.Feed([]byte{0xAA}, base)
	// 10ms 后又来一段：同一帧（IDLE 未到）
	_, _ = f.Feed([]byte{0xBB, 0xCC}, base.Add(10*time.Millisecond))
	got := f.FlushIdle(base.Add(35 * time.Millisecond))
	if len(got) != 1 || len(got[0]) != 3 {
		t.Fatalf("got %v, want one 3-byte frame", got)
	}
}

func TestDetectFrameSplitsStickyPackets(t *testing.T) {
	// 协议：0xAA 帧头 + 长度字节(len) + body，总长 = len+2
	detect := func(buf []byte) (int, error) {
		if len(buf) < 2 || buf[0] != 0xAA {
			return 1, nil // 丢掉非帧头字节
		}
		total := int(buf[1]) + 2
		if total > len(buf) {
			return 0, nil
		}
		return total, nil
	}
	f := New(0, 0, detect)
	// 两帧粘在一起
	data := []byte{0xAA, 0x01, 0x11, 0xAA, 0x02, 0x22, 0x33}
	got, err := f.Feed(data, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d frames: %v", len(got), got)
	}
	if len(got[0]) != 3 || got[0][2] != 0x11 {
		t.Fatalf("frame0 = % X", got[0])
	}
	if len(got[1]) != 4 || got[1][3] != 0x33 {
		t.Fatalf("frame1 = % X", got[1])
	}
	if f.Pending() != 0 {
		t.Fatalf("pending = %d", f.Pending())
	}
}

func TestDetectFrameWaitsForPartial(t *testing.T) {
	detect := func(buf []byte) (int, error) {
		if len(buf) < 2 {
			return 0, nil
		}
		total := int(buf[1]) + 2
		if total > len(buf) {
			return 0, nil
		}
		return total, nil
	}
	f := New(0, 0, detect)
	got, err := f.Feed([]byte{0xAA, 0x04, 0x01}, time.Now())
	if err != nil || len(got) != 0 {
		t.Fatalf("partial should not emit: %v %v", got, err)
	}
	if f.Pending() != 3 {
		t.Fatalf("pending = %d, want 3", f.Pending())
	}
	got, err = f.Feed([]byte{0x02, 0x03, 0x04}, time.Now())
	if err != nil || len(got) != 1 || len(got[0]) != 6 {
		t.Fatalf("completed frame = %v, %v", got, err)
	}
}

func TestDetectFrameOversizeForced(t *testing.T) {
	// 脚本永远说「数据不足」：达到 maxFrame 必须强制切出，不能无限缓冲
	detect := func(buf []byte) (int, error) { return 0, nil }
	f := New(0, 16, detect)
	got, err := f.Feed(make([]byte, 40), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("forced frames = %d, want 2 (16+16)", len(got))
	}
	if f.Pending() != 8 {
		t.Fatalf("pending = %d, want 8", f.Pending())
	}
}

func TestIdleOversizeForced(t *testing.T) {
	f := New(20*time.Millisecond, 8, nil)
	got, err := f.Feed(make([]byte, 20), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || len(got[0]) != 8 {
		t.Fatalf("got %d frames", len(got))
	}
	if f.Pending() != 4 {
		t.Fatalf("pending = %d, want 4", f.Pending())
	}
}

func TestDetectErrorDropsBuffer(t *testing.T) {
	f := New(0, 0, func([]byte) (int, error) { return 0, errors.New("boom") })
	if _, err := f.Feed([]byte{1, 2, 3}, time.Now()); err == nil {
		t.Fatal("err = nil")
	}
	if f.Pending() != 0 {
		t.Fatalf("buffer must be dropped on detect error, pending = %d", f.Pending())
	}
}

func TestDetectBadBoundary(t *testing.T) {
	f := New(0, 64, func([]byte) (int, error) { return -3, nil })
	if _, err := f.Feed([]byte{1}, time.Now()); !errors.Is(err, ErrBadBoundary) {
		t.Fatalf("err = %v, want ErrBadBoundary", err)
	}
	if f.Pending() != 0 {
		t.Fatal("buffer should be dropped")
	}
}

func TestResetClearsPending(t *testing.T) {
	f := New(0, 0, func([]byte) (int, error) { return 0, nil })
	_, _ = f.Feed([]byte{1, 2, 3}, time.Now())
	f.Reset()
	if f.Pending() != 0 {
		t.Fatalf("pending after reset = %d", f.Pending())
	}
}
