package serial

import "testing"

func TestEnumerateDoesNotError(t *testing.T) {
	ports, err := Enumerate()
	if err != nil {
		t.Fatal(err)
	}
	// 无硬件环境列表可为空，只验证调用成功
	for _, p := range ports {
		if p.Name == "" {
			t.Error("port name must not be empty")
		}
	}
}

func TestOpenRejectsInvalidConfig(t *testing.T) {
	p := NewPort()
	cases := []Config{
		{PortName: "COM3", BaudRate: 0, DataBits: 8, StopBits: 1, Parity: "N", FlowControl: "none"},
		{PortName: "", BaudRate: 115200, DataBits: 8, StopBits: 1, Parity: "N", FlowControl: "none"},
		{PortName: "COM3", BaudRate: 115200, DataBits: 8, StopBits: 1, Parity: "N", FlowControl: "bogus"},
		{PortName: "COM3", BaudRate: 115200, DataBits: 8, StopBits: 1, Parity: "X", FlowControl: "none"},
	}
	for i, c := range cases {
		if err := p.Open(c); err == nil {
			t.Errorf("case %d: want error, got nil", i)
		}
	}
	if p.IsOpen() {
		t.Error("port must stay closed after failed opens")
	}
}

func TestCloseWhenClosedIsNoop(t *testing.T) {
	p := NewPort()
	if err := p.Close(); err != nil {
		t.Errorf("close on closed port = %v", err)
	}
}
