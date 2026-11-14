package app

import (
	"context"
	"time"

	"seriallens/internal/serial"
)

// hotplugIntervalMs 串口热插拔轮询间隔。
// Windows 上没有可直接消费的跨库插拔事件（RegisterDeviceNotification 需要挂
// 窗口消息且不跨平台），而枚举本身只是注册表查询、开销极低，因此用轮询 +
// 变化才广播：实现简单、跨平台一致，且服务端/客户端（AI 工具）都能受益。
const hotplugIntervalMs = 2000

// StartHotplugWatch 启动串口插拔监听：列表变化时广播 serial:ports。
// ctx 由 Wails 服务生命周期管理（应用退出时 cancel）。
func (s *SerialService) StartHotplugWatch(ctx context.Context) {
	if ctx == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(hotplugIntervalMs)
		defer ticker.Stop()
		var last string
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			ports, err := serial.Enumerate()
			if err != nil {
				continue // 枚举失败保持上次结果，不打断
			}
			sig := portSignature(ports)
			s.mu.Lock()
			changed := sig != last
			last = sig
			emit := s.emit
			s.mu.Unlock()
			if changed && emit != nil {
				emit("serial:ports", map[string]any{"ports": ports})
			}
		}
	}()
}

// portSignature 把端口列表压成一个可比较的签名（含 VID/PID 与友好名，
// 同名不同设备的更换也能触发刷新）。
func portSignature(ports []serial.PortInfo) string {
	sig := ""
	for _, p := range ports {
		sig += p.Name + "|" + p.FriendlyName + "|" + p.VID + "|" + p.PID + ";"
	}
	return sig
}
