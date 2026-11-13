-- SerialLens 协议解析脚本（空白模板）
-- metadata: {"name":"blank","version":"1.0","author":""}
-- commands: {}
--
-- 内置 API 摘要（完整文档见 docs）：
--   on_frame(raw)            必须实现：解码一帧，返回字段 table；失败返回 nil, "错误信息"
--   encode_command(cmd, p)   可选：编码下发命令，返回完整帧 string
--   detect_frame(buf)        可选：自定义帧边界，返回帧长度 int（默认 IDLE 20ms 切帧）
--   emit_chart(channel, v)   推送图表数据点，通道名是 charts.json 的事实来源
--   send_bytes(s)            脚本主动向串口写数据
--   log(msg, level?)         输出到脚本控制台
--   crc16_modbus / crc16_ccitt / sum8 / xor8(s)     校验 helper
--   band/bor/bnot/blshift/brshift(...)              位运算 helper（Lua 5.1 无位运算符）
--   bytes_to_hex / hex_to_bytes(s)                  字节互转
--   struct_float32_be/le(s, off) struct_int16/32_be/le(s, off)  多字节读取
--   os_time_ms()             毫秒时间戳
--
-- 沙箱已禁用 os/io/require/dofile/loadfile。

-- 必须：解码入口，raw 为 string
function on_frame(raw)
    -- TODO: 实现协议解码
    return nil, "not implemented"
end

-- 可选：编码入口，cmd 为命令名，params 为参数表，返回完整帧(含 CRC) string
-- function encode_command(cmd, params) ... end

-- 可选：自定义帧边界，buf 为 string，返回本帧字节数
-- function detect_frame(buf) return #buf end
