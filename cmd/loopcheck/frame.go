package main

import "encoding/binary"

// 定长帧：4B 小端序号 + 60B 由序号派生的内容。
// 定长便于在字节流里滑动定位丢帧位置，内容可独立重算以检出内容错配。
const (
	FrameSize  = 64
	PayloadLen = FrameSize - 4
)

// Frame 帧结构。
type Frame struct {
	Seq     uint32
	Payload [PayloadLen]byte
}

// lcgSeed 序号到伪随机流的确定性种子（发送端与校验端必须一致）。
func lcgSeed(seq uint32) uint32 {
	return seq*1664525 + 1013904223
}

// lcgNext 线性同余生成器（数值常量取 Numerical Recipes 参数）。
func lcgNext(x uint32) uint32 {
	return x*1664525 + 1013904223
}

// DerivePayload 由序号派生 60B 内容，发送端与接收端各自计算，无需传输校验和。
func DerivePayload(seq uint32, out []byte) {
	x := lcgSeed(seq)
	for i := 0; i < len(out) && i < PayloadLen; i++ {
		x = lcgNext(x)
		out[i] = byte(x >> 24)
	}
}

// EncodeFrame 将 seq 编码进 dst 的前 FrameSize 字节；dst 不足时不做任何写入。
func EncodeFrame(dst []byte, seq uint32) bool {
	if len(dst) < FrameSize {
		return false
	}
	binary.LittleEndian.PutUint32(dst[:4], seq)
	frame := Frame{Seq: seq}
	DerivePayload(seq, frame.Payload[:])
	copy(dst[4:FrameSize], frame.Payload[:])
	return true
}

// DecodeFrame 解析 FrameSize 字节为一帧（不做内容校验）。
func DecodeFrame(src []byte) (Frame, bool) {
	if len(src) < FrameSize {
		return Frame{}, false
	}
	f := Frame{Seq: binary.LittleEndian.Uint32(src[:4])}
	copy(f.Payload[:], src[4:FrameSize])
	return f, true
}

// VerifyFrame 重算内容指纹，判断载荷是否与序号一致。
func VerifyFrame(f Frame) bool {
	var expect [PayloadLen]byte
	DerivePayload(f.Seq, expect[:])
	for i := range expect {
		if expect[i] != f.Payload[i] {
			return false
		}
	}
	return true
}
