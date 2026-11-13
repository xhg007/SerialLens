package parser

import (
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"seriallens/internal/events"
	"seriallens/internal/lua"
)

// TestSoak1Mbps PRD §8 M2 验收：1Mbps 持续 10 分钟不崩。
// 正常测试跑 5 秒；验收时用 SL_SOAK_SECONDS=600 跑满 10 分钟。
//
//	SL_SOAK_SECONDS=600 go test ./internal/parser -run TestSoak1Mbps -v
func TestSoak1Mbps(t *testing.T) {
	if testing.Short() {
		t.Skip("soak skipped in -short")
	}
	seconds := 5
	if v := os.Getenv("SL_SOAK_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			t.Fatalf("bad SL_SOAK_SECONDS=%q", v)
		}
		seconds = n
	}

	var parsed int64
	rec := &recorder{}
	rt := lua.New(lua.Options{PoolSize: 4, Timeout: time.Second})
	t.Cleanup(rt.Close)
	p := New(Options{
		Runtime: rt,
		Emit: func(name string, payload map[string]any) {
			if name == events.EvFrameParsed {
				atomic.AddInt64(&parsed, 1)
			}
			rec.emit(name, payload)
		},
	})
	p.Start()
	t.Cleanup(p.Stop)

	// 固定 100 字节帧：1Mbps 8N1 = 100_000 B/s ⇒ 1000 帧/秒。
	src := `
function detect_frame(buf)
    if #buf < 100 then return 0 end
    return 100
end
function on_frame(raw)
    emit_chart("v", struct_uint16_be(raw, 0))
    return { v = struct_uint16_be(raw, 0), crc = band(crc16_modbus(raw), 0xFFFF) }
end
`
	if err := p.LoadScriptSource("soak.lua", []byte(src)); err != nil {
		t.Fatal(err)
	}

	frame := make([]byte, 100)
	for i := range frame {
		frame[i] = byte(i)
	}

	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	var sent int64
	for time.Now().Before(deadline) {
		<-ticker.C
		p.Ingest(frame)
		atomic.AddInt64(&sent, 1)
	}
	// 等尾部帧落盘
	time.Sleep(100 * time.Millisecond)

	stats := p.Stats()
	t.Logf("soak %ds: sent=%d parsed=%d errors=%v dropped=%v queue=%v/%v",
		seconds, atomic.LoadInt64(&sent), atomic.LoadInt64(&parsed),
		stats["errors"], stats["dropped"], stats["queue_len"], stats["queue_cap"])

	if stats["errors"].(uint64) != 0 {
		t.Fatalf("errors during soak: %v (last=%v)", stats["errors"], stats["last_error"])
	}
	if atomic.LoadInt64(&parsed) == 0 {
		t.Fatal("no frames parsed during soak")
	}
	// 1000 帧/秒的发送速率下，解析不允许明显落后（允许 5% 抖动）
	want := float64(atomic.LoadInt64(&sent)) * 0.95
	if float64(atomic.LoadInt64(&parsed)) < want {
		t.Fatalf("parsed %d < 95%% of sent %d", parsed, sent)
	}
}
