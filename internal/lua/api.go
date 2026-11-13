package lua

import (
	"encoding/binary"
	"encoding/hex"
	"math"
	"strings"
	"time"

	glua "github.com/yuin/gopher-lua"
)

// registerAPI 注册内置 API（PRD §F3 v1 全量）。
func registerAPI(L *glua.LState, sinks Sinks) {
	L.SetGlobal("crc16_modbus", L.NewFunction(luaCRC16Modbus))
	L.SetGlobal("crc16_ccitt", L.NewFunction(luaCRC16CCITT))
	L.SetGlobal("sum8", L.NewFunction(luaSum8))
	L.SetGlobal("xor8", L.NewFunction(luaXor8))

	L.SetGlobal("band", L.NewFunction(func(L *glua.LState) int {
		L.Push(glua.LNumber(int64(L.CheckInt64(1)) & int64(L.CheckInt64(2))))
		return 1
	}))
	L.SetGlobal("bor", L.NewFunction(func(L *glua.LState) int {
		L.Push(glua.LNumber(int64(L.CheckInt64(1)) | int64(L.CheckInt64(2))))
		return 1
	}))
	L.SetGlobal("bnot", L.NewFunction(func(L *glua.LState) int {
		L.Push(glua.LNumber(^int64(L.CheckInt64(1))))
		return 1
	}))
	L.SetGlobal("blshift", L.NewFunction(func(L *glua.LState) int {
		L.Push(glua.LNumber(int64(L.CheckInt64(1)) << uint(L.CheckInt64(2))))
		return 1
	}))
	L.SetGlobal("brshift", L.NewFunction(func(L *glua.LState) int {
		// 逻辑右移（Lua 侧用于无符号字段）
		v := uint64(L.CheckInt64(1))
		L.Push(glua.LNumber(v >> uint(L.CheckInt64(2))))
		return 1
	}))

	L.SetGlobal("bytes_to_hex", L.NewFunction(func(L *glua.LState) int {
		b, _ := luaValueToBytes(L.Get(1))
		L.Push(glua.LString(strings.ToUpper(hex.EncodeToString(b))))
		return 1
	}))
	L.SetGlobal("hex_to_bytes", L.NewFunction(func(L *glua.LState) int {
		clean := strings.Join(strings.Fields(L.CheckString(1)), "")
		b, err := hex.DecodeString(clean)
		if err != nil {
			L.RaiseError("hex_to_bytes: %v", err)
			return 0
		}
		L.Push(glua.LString(string(b)))
		return 1
	}))

	L.SetGlobal("os_time_ms", L.NewFunction(func(L *glua.LState) int {
		L.Push(glua.LNumber(time.Now().UnixMilli()))
		return 1
	}))

	// struct_* 读取器：fn(string, offset) -> number
	L.SetGlobal("struct_int8", L.NewFunction(structInt8))
	L.SetGlobal("struct_uint8", L.NewFunction(structUint8))
	L.SetGlobal("struct_int16_be", L.NewFunction(structInt16BE))
	L.SetGlobal("struct_int16_le", L.NewFunction(structInt16LE))
	L.SetGlobal("struct_uint16_be", L.NewFunction(structUint16BE))
	L.SetGlobal("struct_uint16_le", L.NewFunction(structUint16LE))
	L.SetGlobal("struct_int32_be", L.NewFunction(structInt32BE))
	L.SetGlobal("struct_int32_le", L.NewFunction(structInt32LE))
	L.SetGlobal("struct_uint32_be", L.NewFunction(structUint32BE))
	L.SetGlobal("struct_uint32_le", L.NewFunction(structUint32LE))
	L.SetGlobal("struct_float32_be", L.NewFunction(structFloat32BE))
	L.SetGlobal("struct_float32_le", L.NewFunction(structFloat32LE))

	L.SetGlobal("log", L.NewFunction(func(L *glua.LState) int {
		msg := L.CheckString(1)
		level := "info"
		if L.GetTop() >= 2 && L.Get(2) != glua.LNil {
			level = strings.ToLower(L.CheckString(2))
		}
		sinks.Log(level, msg)
		return 0
	}))

	L.SetGlobal("emit_chart", L.NewFunction(func(L *glua.LState) int {
		channel := L.CheckString(1)
		value := L.CheckNumber(2)
		if c, ok := L.Context().Value(collectorKey).(*chartCollector); ok && c != nil {
			c.points = append(c.points, ChartPoint{Channel: channel, Value: float64(value)})
		}
		return 0
	}))

	L.SetGlobal("send_bytes", L.NewFunction(func(L *glua.LState) int {
		data, ok := luaValueToBytes(L.Get(1))
		if !ok {
			L.RaiseError("send_bytes: argument must be string")
			return 0
		}
		if sinks.Send != nil {
			if err := sinks.Send(data); err != nil {
				L.RaiseError("send_bytes: %v", err)
			}
		}
		return 0
	}))
}

// checkOffset 取 string 与 offset，越界直接抛 Lua 错误（AI 生成脚本更易定位）。
func checkOffset(L *glua.LState) (string, int) {
	s := L.CheckString(1)
	off := L.CheckInt(2)
	if off < 0 || off >= len(s) {
		L.RaiseError("offset %d out of range (len=%d)", off, len(s))
	}
	return s, off
}

func luaCRC16Modbus(L *glua.LState) int {
	data, ok := luaValueToBytes(L.Get(1))
	if !ok {
		L.RaiseError("crc16_modbus: argument must be string")
		return 0
	}
	L.Push(glua.LNumber(CRC16Modbus(data)))
	return 1
}

func luaCRC16CCITT(L *glua.LState) int {
	data, ok := luaValueToBytes(L.Get(1))
	if !ok {
		L.RaiseError("crc16_ccitt: argument must be string")
		return 0
	}
	L.Push(glua.LNumber(CRC16CCITT(data)))
	return 1
}

func luaSum8(L *glua.LState) int {
	data, ok := luaValueToBytes(L.Get(1))
	if !ok {
		L.RaiseError("sum8: argument must be string")
		return 0
	}
	var sum byte
	for _, b := range data {
		sum += b
	}
	L.Push(glua.LNumber(sum))
	return 1
}

func luaXor8(L *glua.LState) int {
	data, ok := luaValueToBytes(L.Get(1))
	if !ok {
		L.RaiseError("xor8: argument must be string")
		return 0
	}
	var x byte
	for _, b := range data {
		x ^= b
	}
	L.Push(glua.LNumber(x))
	return 1
}

func structInt8(L *glua.LState) int {
	s, off := checkOffset(L)
	L.Push(glua.LNumber(int8(s[off])))
	return 1
}

func structUint8(L *glua.LState) int {
	s, off := checkOffset(L)
	L.Push(glua.LNumber(s[off]))
	return 1
}

func structInt16BE(L *glua.LState) int {
	s, off := checkOffset(L)
	L.Push(glua.LNumber(int16(binary.BigEndian.Uint16([]byte(s[off:])))))
	return 1
}

func structInt16LE(L *glua.LState) int {
	s, off := checkOffset(L)
	L.Push(glua.LNumber(int16(binary.LittleEndian.Uint16([]byte(s[off:])))))
	return 1
}

func structUint16BE(L *glua.LState) int {
	s, off := checkOffset(L)
	L.Push(glua.LNumber(binary.BigEndian.Uint16([]byte(s[off:]))))
	return 1
}

func structUint16LE(L *glua.LState) int {
	s, off := checkOffset(L)
	L.Push(glua.LNumber(binary.LittleEndian.Uint16([]byte(s[off:]))))
	return 1
}

func structInt32BE(L *glua.LState) int {
	s, off := checkOffset(L)
	L.Push(glua.LNumber(int32(binary.BigEndian.Uint32([]byte(s[off:])))))
	return 1
}

func structInt32LE(L *glua.LState) int {
	s, off := checkOffset(L)
	L.Push(glua.LNumber(int32(binary.LittleEndian.Uint32([]byte(s[off:])))))
	return 1
}

func structUint32BE(L *glua.LState) int {
	s, off := checkOffset(L)
	L.Push(glua.LNumber(binary.BigEndian.Uint32([]byte(s[off:]))))
	return 1
}

func structUint32LE(L *glua.LState) int {
	s, off := checkOffset(L)
	L.Push(glua.LNumber(binary.LittleEndian.Uint32([]byte(s[off:]))))
	return 1
}

func structFloat32BE(L *glua.LState) int {
	s, off := checkOffset(L)
	L.Push(glua.LNumber(math.Float32frombits(binary.BigEndian.Uint32([]byte(s[off:])))))
	return 1
}

func structFloat32LE(L *glua.LState) int {
	s, off := checkOffset(L)
	L.Push(glua.LNumber(math.Float32frombits(binary.LittleEndian.Uint32([]byte(s[off:])))))
	return 1
}

// CRC16Modbus CRC-16/MODBUS（poly 0xA001 反射，init 0xFFFF）。
func CRC16Modbus(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// CRC16CCITT CRC-16/CCITT-FALSE（poly 0x1021，init 0xFFFF）。
func CRC16CCITT(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}
