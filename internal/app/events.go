package app

// 事件契约常量与 Emitter 的唯一事实来源是 internal/events（因 project 包
// 不能依赖 app，避免循环）。此处 re-export 保持壳层引用便利。

import "seriallens/internal/events"

type Emitter = events.Emitter

const (
	EvProjectOpened = events.EvProjectOpened
	EvProjectClosed = events.EvProjectClosed
	EvProjectError  = events.EvProjectError
	EvRecentUpdated = events.EvRecentUpdated
	EvSerialStatus  = events.EvSerialStatus
	EvSerialData    = events.EvSerialData
)
