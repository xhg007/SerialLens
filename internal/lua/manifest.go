package lua

// APIDoc 一条脚本 API 说明。前端用它生成 Monaco 自动补全与悬停文档，
// 与 api.go 的注册表同源（新增内置函数时两处一起改，manifest_test 会兜底比对）。
type APIDoc struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`    // function | callback
	Call    string `json:"call"`    // 插入的代码片段，如 "crc16_modbus(data)"
	Doc     string `json:"doc"`     // 一行说明
	Returns string `json:"returns"` // 返回类型说明
}

// APIManifest 脚本可用的全部内置 API 与回调（PRD §F3 + F4 自动补全）。
func APIManifest() []APIDoc {
	return []APIDoc{
		{Name: "on_frame", Kind: "callback", Call: "on_frame(raw)", Doc: "每切出一帧调用一次，返回字段表（table）；其中数值字段可被图表引用", Returns: "table | nil"},
		{Name: "detect_frame", Kind: "callback", Call: "detect_frame(buf)", Doc: "可选：返回本帧字节数，返回 0 表示继续等待；不定义则用 IDLE 20ms 切帧", Returns: "number"},
		{Name: "encode_command", Kind: "callback", Call: "encode_command(cmd, params)", Doc: "可选：把命令编码成完整字节串（含 CRC），供命令面板下发", Returns: "string"},
		{Name: "emit_chart", Kind: "function", Call: `emit_chart("channel", value)`, Doc: "向图表推一个数据点（33ms 合批下发），通道名任意", Returns: "nil"},
		{Name: "send_bytes", Kind: "function", Call: "send_bytes(data)", Doc: "直接从串口发送二进制数据（字符串按字节解释）", Returns: "nil"},
		{Name: "log", Kind: "function", Call: `log("message", "info")`, Doc: "输出到脚本控制台；level 可为 info/debug/warn/error", Returns: "nil"},
		{Name: "crc16_modbus", Kind: "function", Call: "crc16_modbus(data)", Doc: "CRC-16/MODBUS（多项式 0xA001，初值 0xFFFF）", Returns: "number"},
		{Name: "crc16_ccitt", Kind: "function", Call: "crc16_ccitt(data)", Doc: "CRC-16/CCITT-FALSE", Returns: "number"},
		{Name: "sum8", Kind: "function", Call: "sum8(data)", Doc: "8 位累加和", Returns: "number"},
		{Name: "xor8", Kind: "function", Call: "xor8(data)", Doc: "8 位异或校验", Returns: "number"},
		{Name: "band", Kind: "function", Call: "band(a, b)", Doc: "按位与（Lua 5.1 无位运算）", Returns: "number"},
		{Name: "bor", Kind: "function", Call: "bor(a, b)", Doc: "按位或", Returns: "number"},
		{Name: "bnot", Kind: "function", Call: "bnot(a)", Doc: "按位取反", Returns: "number"},
		{Name: "blshift", Kind: "function", Call: "blshift(a, n)", Doc: "左移", Returns: "number"},
		{Name: "brshift", Kind: "function", Call: "brshift(a, n)", Doc: "右移", Returns: "number"},
		{Name: "bytes_to_hex", Kind: "function", Call: "bytes_to_hex(data)", Doc: "字节串转大写 HEX 字符串", Returns: "string"},
		{Name: "hex_to_bytes", Kind: "function", Call: `hex_to_bytes("AABB")`, Doc: "HEX 字符串转字节串（忽略空白）", Returns: "string"},
		{Name: "os_time_ms", Kind: "function", Call: "os_time_ms()", Doc: "当前毫秒时间戳", Returns: "number"},
		{Name: "struct_int8", Kind: "function", Call: "struct_int8(data, offset)", Doc: "读取有符号 8 位整数（offset 从 0 开始）", Returns: "number"},
		{Name: "struct_uint8", Kind: "function", Call: "struct_uint8(data, offset)", Doc: "读取无符号 8 位整数", Returns: "number"},
		{Name: "struct_int16_be", Kind: "function", Call: "struct_int16_be(data, offset)", Doc: "读取大端有符号 16 位整数", Returns: "number"},
		{Name: "struct_int16_le", Kind: "function", Call: "struct_int16_le(data, offset)", Doc: "读取小端有符号 16 位整数", Returns: "number"},
		{Name: "struct_uint16_be", Kind: "function", Call: "struct_uint16_be(data, offset)", Doc: "读取大端无符号 16 位整数", Returns: "number"},
		{Name: "struct_uint16_le", Kind: "function", Call: "struct_uint16_le(data, offset)", Doc: "读取小端无符号 16 位整数", Returns: "number"},
		{Name: "struct_int32_be", Kind: "function", Call: "struct_int32_be(data, offset)", Doc: "读取大端有符号 32 位整数", Returns: "number"},
		{Name: "struct_int32_le", Kind: "function", Call: "struct_int32_le(data, offset)", Doc: "读取小端有符号 32 位整数", Returns: "number"},
		{Name: "struct_uint32_be", Kind: "function", Call: "struct_uint32_be(data, offset)", Doc: "读取大端无符号 32 位整数", Returns: "number"},
		{Name: "struct_uint32_le", Kind: "function", Call: "struct_uint32_le(data, offset)", Doc: "读取小端无符号 32 位整数", Returns: "number"},
		{Name: "struct_float32_be", Kind: "function", Call: "struct_float32_be(data, offset)", Doc: "读取大端 IEEE-754 单精度浮点", Returns: "number"},
		{Name: "struct_float32_le", Kind: "function", Call: "struct_float32_le(data, offset)", Doc: "读取小端 IEEE-754 单精度浮点", Returns: "number"},
	}
}
