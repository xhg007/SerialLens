package serial

import (
	"fmt"

	"go.bug.st/serial"
)

// 校验辅助：参数在打开端口前校验，避免依赖真实设备才能暴露配置错误。
func validateConfig(cfg Config) error {
	if cfg.PortName == "" {
		return fmt.Errorf("port name is empty")
	}
	if cfg.BaudRate < 1200 || cfg.BaudRate > 10000000 {
		return fmt.Errorf("baudrate %d out of range [1200, 10000000]", cfg.BaudRate)
	}
	switch cfg.Parity {
	case "N", "E", "O":
	default:
		return fmt.Errorf("invalid parity %q", cfg.Parity)
	}
	switch cfg.FlowControl {
	case "none", "rtscts", "xonxoff":
	default:
		return fmt.Errorf("invalid flow control %q", cfg.FlowControl)
	}
	return nil
}

// SerialPort go.bug.st 实现。
type SerialPort struct {
	port serial.Port
}

// NewPort 返回系统串口实现。
func NewPort() Port {
	return &SerialPort{}
}

func mapParity(s string) serial.Parity {
	switch s {
	case "E":
		return serial.EvenParity
	case "O":
		return serial.OddParity
	default:
		return serial.NoParity
	}
}

func (s *SerialPort) Open(cfg Config) error {
	if err := validateConfig(cfg); err != nil {
		return err
	}
	if cfg.FlowControl != "none" {
		// go.bug.st v1.8.0 无流控 API；配置层保留 rtscts/xonxoff 以兼容工程配置，
		// 打开时显式报错而非静默忽略。
		return fmt.Errorf("flow control %q not supported yet; use none", cfg.FlowControl)
	}
	if s.port != nil {
		_ = s.port.Close()
	}
	mode := &serial.Mode{
		BaudRate: cfg.BaudRate,
		DataBits: cfg.DataBits,
		Parity:   mapParity(cfg.Parity),
		StopBits: stopBits(cfg.StopBits),
	}
	port, err := serial.Open(cfg.PortName, mode)
	if err != nil {
		return err
	}
	s.port = port
	return nil
}

func stopBits(n int) serial.StopBits {
	switch n {
	case 2:
		return serial.TwoStopBits
	case 15:
		return serial.OnePointFiveStopBits
	default:
		return serial.OneStopBit
	}
}

func (s *SerialPort) Close() error {
	if s.port == nil {
		return nil
	}
	err := s.port.Close()
	s.port = nil
	return err
}

func (s *SerialPort) Read(p []byte) (int, error) {
	if s.port == nil {
		return 0, fmt.Errorf("port not open")
	}
	return s.port.Read(p)
}

func (s *SerialPort) Write(p []byte) (int, error) {
	if s.port == nil {
		return 0, fmt.Errorf("port not open")
	}
	return s.port.Write(p)
}

func (s *SerialPort) IsOpen() bool {
	return s.port != nil
}

// Enumerate 枚举系统串口（含 USB 热插拔后的新端口）。
func Enumerate() ([]PortInfo, error) {
	names, err := serial.GetPortsList()
	if err != nil {
		return nil, err
	}
	out := make([]PortInfo, 0, len(names))
	for _, n := range names {
		out = append(out, PortInfo{Name: n, FriendlyName: n})
	}
	return out, nil
}
