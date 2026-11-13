// Package events 前后端事件契约（唯一事实来源，PRD §7.4）。
// 独立叶包：internal/project 等内部服务需在 import app 之前引用这些常量，
// 避免 project→app 循环依赖。internal/app 对其 re-export 供壳层使用。
package events

// Emitter 是内部 Service 向前端推事件的单向通道抽象。
// main.go 用 app.Event.Emit 桥接实现；测试用闭包捕获。
type Emitter func(event string, payload map[string]any)

// M0/M1 子集；后续里程碑在此追加。
const (
	// 工程
	EvProjectOpened = "project:opened" // payload: {path,name,serial_config(map[string]any),active_script}
	EvProjectClosed = "project:closed" // payload: {}
	EvProjectError  = "project:error"  // payload: {code,message}
	EvRecentUpdated = "recent:updated" // payload: {projects:[{path,name,last_opened,pinned,missing}]}

	// 串口与数据
	EvSerialStatus = "serial:status" // payload: {state:"disconnected"|"connected", bytes_in, bytes_out}
	EvSerialData   = "serial:data"   // payload: {hex:"AABB..", ts:毫秒时间戳} 33ms 合帧批量

	// 解析（M2）
	EvFrameParsed = "frame:parsed" // payload: {hex, ts, fields(map), duration_us}
	EvChartPoint  = "chart:point"  // payload: {points:[{ch,v,ts}]} 33ms 合批

	// 脚本（M2）
	EvScriptReloaded = "script:reloaded" // payload: {name, source:"ui"|"external", compile_ms, load_ms}
	EvScriptError    = "script:error"    // payload: {script, error, line, column}（1Hz 节流）
	EvScriptLog      = "script:log"      // payload: {level, msg, ts}

	// 编辑器（M4）
	EvScriptChanged = "script:changed" // payload: {name, content, source:"ui"|"external", removed?}
	EvScriptList    = "script:list"    // payload: {files:[{name,size,mod_time,active,has_error}]}

	// 图表（M3）
	EvChartsUpdated = "charts:updated" // payload: {config(map), source:"project"|"external"|"ui"}
	EvChartsError   = "charts:error"   // payload: {message} 外部编辑非法时不落盘、保留旧配置

	// AI 助手（M5）
	EvAIStatus = "ai:status" // payload: {running, ready, provider, model, has_key, messages}
	EvAIStart  = "ai:start"  // payload: {id, provider, model}
	EvAIDelta  = "ai:delta"  // payload: {id, text, reasoning}（~60ms 合批）
	EvAIDone   = "ai:done"   // payload: {id, content, reasoning, usage, elapsed_ms, first_ms, finish_reason, canceled}
	EvAIError  = "ai:error"  // payload: {id, message}
)
