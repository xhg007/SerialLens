package serial

// Port 总线抽象（PRD §7.4，v2 总线扩展预留：CAN/USB 等实现同一接口）。
type Port interface {
	Open(cfg Config) error
	Close() error
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	IsOpen() bool
}

// Config 串口连接参数（与 project.SerialConfig 字段对应）。
type Config struct {
	PortName    string
	BaudRate    int
	DataBits    int
	StopBits    int
	Parity      string // N / E / O
	FlowControl string // none / rtscts / xonxoff
}

// PortInfo 枚举结果。
type PortInfo struct {
	Name         string
	FriendlyName string
	VID          string
	PID          string
}
