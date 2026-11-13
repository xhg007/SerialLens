// loopcheck 是 M1 验收工具：用生产代码链路（SerialService + 33ms 合帧）
// 校验 115200 下回环收发是否零丢包，并输出吞吐与事件间隔。
//
// 用法（真实虚拟串口对）：
//
//	go run ./cmd/loopcheck -tx COM11 -rx COM12 -baud 115200 -rate 11520 -bytes 691200
//
// 降级（无驱动）：
//
//	go run ./cmd/loopcheck -loopback -rate 11520 -bytes 691200
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"time"

	"seriallens/internal/app"
	"seriallens/internal/events"
	"seriallens/internal/serial"
)

// Options 验收参数。
type Options struct {
	TXPort     string
	RXPort     string
	Baud       int
	Rate       int // 字节/秒；0 表示不间断突发
	TotalBytes int
	Chunk      int
	Drain      time.Duration
	Warmup     time.Duration
	Loopback   bool
	List       bool
	SendOnly   bool
}

func main() {
	var o Options
	flag.StringVar(&o.TXPort, "tx", "COM11", "发送端端口")
	flag.StringVar(&o.RXPort, "rx", "COM12", "接收端端口")
	flag.IntVar(&o.Baud, "baud", 115200, "波特率")
	flag.IntVar(&o.Rate, "rate", 11520, "发送速率（字节/秒），0=突发")
	flag.IntVar(&o.TotalBytes, "bytes", 691200, "总发送字节数（默认 115200 下 60 秒）")
	flag.IntVar(&o.Chunk, "chunk", 512, "单次 Write 大小（自动对齐到 64B 帧）")
	flag.DurationVar(&o.Drain, "drain", 10*time.Second, "发送完成后等待收齐的时限")
	flag.DurationVar(&o.Warmup, "warmup", 300*time.Millisecond, "发送前等待接收端读循环就绪")
	flag.BoolVar(&o.Loopback, "loopback", false, "降级为进程内内存管道（不经过驱动）")
	flag.BoolVar(&o.List, "list", false, "列出系统串口后退出")
	flag.BoolVar(&o.SendOnly, "sendonly", false, "只发送不接收（供 UI 冻结实测，接收端为界面自身）")
	flag.Parse()

	if o.List {
		ports, err := serial.Enumerate()
		if err != nil {
			fmt.Fprintf(os.Stderr, "enumerate: %v\n", err)
			os.Exit(1)
		}
		for _, p := range ports {
			fmt.Printf("%-10s %s\n", p.Name, p.FriendlyName)
		}
		return
	}

	if err := run(o); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
		os.Exit(1)
	}
}

func run(o Options) error {
	if o.SendOnly {
		return runSendOnly(o)
	}
	tx, rx, mode := openPorts(o)
	defer tx.Close()

	col := &Collector{}
	svc := app.NewSerialService(rx, func(event string, payload map[string]any) {
		if event != events.EvSerialData {
			return
		}
		s, _ := payload["hex"].(string)
		raw, err := hex.DecodeString(s)
		if err != nil {
			col.BadBytes(1)
			return
		}
		col.Feed(raw, time.Now())
	})
	if err := svc.Connect(map[string]any{
		"port": o.RXPort, "baudrate": o.Baud, "data_bits": 8,
		"stop_bits": 1, "parity": "N", "flow_control": "none",
	}); err != nil {
		return fmt.Errorf("open rx %s: %w", o.RXPort, err)
	}
	// 错误路径兜底关闭（正常路径会在下方显式 Disconnect 后再取报告）
	defer func() { _ = svc.Disconnect() }()

	fmt.Printf("== loopcheck (%s) ==\n", mode)
	fmt.Printf("tx=%s rx=%s baud=%d rate=%d B/s bytes=%d chunk=%d\n",
		o.TXPort, o.RXPort, o.Baud, o.Rate, o.TotalBytes, o.Chunk)

	// 接收端读 goroutine 需要时间进入阻塞读；虚拟串口在该窗口内的写入会被驱动丢弃，
	// 因此显式等待，避免把装置启动竞态误判为产品丢包。
	time.Sleep(o.Warmup)

	start := time.Now()
	sent, err := send(tx, o)
	if err != nil {
		return fmt.Errorf("send after %d bytes: %w", sent, err)
	}
	elapsed := time.Since(start)

	// 等待接收端把缓冲区排空（限速模式下发送结束 ≠ 接收结束）
	deadline := time.Now().Add(o.Drain)
	for time.Now().Before(deadline) && col.Received() < sent {
		time.Sleep(20 * time.Millisecond)
	}
	beforeStop := col.Received()

	// 关闭前必须等 readLoop 退出：stop 分支会把 pending 里最后一批 flush 出来，
	// 否则尾部不足一个合帧周期的数据永远无法计入验收。
	if err := svc.Disconnect(); err != nil {
		return fmt.Errorf("disconnect: %w", err)
	}
	fmt.Printf("flush-on-stop: before=%d after=%d (+%d)\n", beforeStop, col.Received(), col.Received()-beforeStop)

	r := col.Report(sent, elapsed)
	printReport(r)
	if !r.Pass() {
		return fmt.Errorf("acceptance failed: missing=%d dup=%d corrupt=%d received=%d/%d",
			r.Missing, r.Dup, r.Corrupt, r.Received, r.Sent)
	}
	fmt.Println("PASS: 0 丢包 / 0 重复 / 0 内容错配")
	return nil
}

// runSendOnly 仅发送：用于 UI 冻结实测，此时接收端由界面自己占用。
func runSendOnly(o Options) error {
	if o.Loopback {
		return fmt.Errorf("-sendonly 不支持 -loopback")
	}
	tx := serial.NewPort()
	cfg := serial.Config{PortName: o.TXPort, BaudRate: o.Baud, DataBits: 8, StopBits: 1, Parity: "N", FlowControl: "none"}
	if err := tx.Open(cfg); err != nil {
		return fmt.Errorf("open tx %s: %w", o.TXPort, err)
	}
	defer func() { _ = tx.Close() }()

	fmt.Printf("== loopcheck send-only ==\ntx=%s baud=%d rate=%d B/s bytes=%d\n",
		o.TXPort, o.Baud, o.Rate, o.TotalBytes)
	time.Sleep(o.Warmup)
	start := time.Now()
	sent, err := send(tx, o)
	elapsed := time.Since(start)
	if err != nil {
		return fmt.Errorf("send after %d bytes: %w", sent, err)
	}
	fmt.Printf("sent=%d elapsed=%s throughput=%.0f B/s\n",
		sent, elapsed.Round(time.Millisecond), float64(sent)/elapsed.Seconds())
	return nil
}

// openPorts 按模式返回发送端、接收端与模式标签。
func openPorts(o Options) (serial.Port, serial.Port, string) {
	if o.Loopback {
		p := newPipe()
		return &loopbackPort{p: p}, &loopbackPort{p: p, owner: true}, "loopback(内存管道，未覆盖驱动)"
	}
	tx := serial.NewPort()
	cfg := serial.Config{PortName: o.TXPort, BaudRate: o.Baud, DataBits: 8, StopBits: 1, Parity: "N", FlowControl: "none"}
	if err := tx.Open(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "open tx %s: %v\n", o.TXPort, err)
		os.Exit(1)
	}
	return tx, serial.NewPort(), "com0com 虚拟串口对"
}

// send 按字节预算限速发送；rate=0 时全速突发。
func send(p serial.Port, o Options) (int, error) {
	chunk := o.Chunk
	if chunk <= 0 {
		chunk = FrameSize
	}
	chunk = ((chunk + FrameSize - 1) / FrameSize) * FrameSize

	total := o.TotalBytes
	if total <= 0 {
		return 0, fmt.Errorf("bytes must be positive")
	}
	total = (total / FrameSize) * FrameSize
	if total == 0 {
		return 0, fmt.Errorf("bytes must be at least %d", FrameSize)
	}

	buf := make([]byte, chunk)
	start := time.Now()
	sent := 0
	var seq uint32
	for sent < total {
		n := chunk
		if remain := total - sent; remain < n {
			n = (remain / FrameSize) * FrameSize
		}
		frames := n / FrameSize
		for i := 0; i < frames; i++ {
			EncodeFrame(buf[i*FrameSize:], seq)
			seq++
		}
		if o.Rate > 0 {
			due := time.Duration(float64(sent+n) / float64(o.Rate) * float64(time.Second))
			sleepUntil(start.Add(due))
		}
		written := 0
		for written < n {
			k, err := p.Write(buf[written:n])
			if err != nil {
				return sent, err
			}
			written += k
		}
		sent += n
	}
	return sent, nil
}

// sleepUntil 分片休眠，避免 Windows 计时器粒度造成长时间阻塞。
func sleepUntil(t time.Time) {
	for {
		d := time.Until(t)
		if d <= 0 {
			return
		}
		if d > 5*time.Millisecond {
			d = 5 * time.Millisecond
		}
		time.Sleep(d)
	}
}

func printReport(r Report) {
	fmt.Println("---- report ----")
	fmt.Printf("sent=%d received=%d events=%d\n", r.Sent, r.Received, r.Events)
	fmt.Printf("frames: ok=%d expected=%d missing=%d dup=%d corrupt=%d\n",
		r.FramesOK, r.FramesExpected, r.Missing, r.Dup, r.Corrupt)
	fmt.Printf("elapsed=%s throughput=%.0f B/s (%.0f bps) maxEventGap=%dms\n",
		r.Elapsed.Round(time.Millisecond), r.ThroughputBps, r.ThroughputBps*8, r.MaxEventGapMs)
}
