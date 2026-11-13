package app

import "testing"

func TestEventContract(t *testing.T) {
	cases := []struct{ name, got, want string }{
		{"EvProjectOpened", EvProjectOpened, "project:opened"},
		{"EvProjectClosed", EvProjectClosed, "project:closed"},
		{"EvProjectError", EvProjectError, "project:error"},
		{"EvRecentUpdated", EvRecentUpdated, "recent:updated"},
		{"EvSerialStatus", EvSerialStatus, "serial:status"},
		{"EvSerialData", EvSerialData, "serial:data"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}
