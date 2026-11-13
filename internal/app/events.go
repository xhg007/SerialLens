package app

// Emitter 是内部 Service 向前端推事件的单向通道抽象。
// main.go 用 app.Event.Emit 桥接实现；测试用闭包捕获。
type Emitter func(event string, payload map[string]any)

// 前后端事件契约（唯一事实来源，PRD §7.4）。
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
)
