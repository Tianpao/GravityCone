package paperconnect

import (
	"reflect"
	"testing"

	"gravitycone/core/easytier"
)

func TestPCGuestPortForwards(t *testing.T) {
	tests := []struct {
		name     string
		mode     easytier.DialMode
		protocol string
		want     []string
	}{
		{
			name:     "proxy nethernet",
			mode:     easytier.DialModeProxy,
			protocol: ProtocolNetherNet,
			want: []string{
				"tcp://127.0.0.1:40001/10.144.144.1:40002",
				"udp://127.0.0.1:40003/10.144.144.1:40004",
			},
		},
		{
			name:     "proxy raknet",
			mode:     easytier.DialModeProxy,
			protocol: ProtocolRakNet,
			want: []string{
				"tcp://127.0.0.1:40001/10.144.144.1:40002",
				"udp://0.0.0.0:40003/10.144.144.1:40004",
			},
		},
		{
			name:     "direct",
			mode:     easytier.DialModeDirect,
			protocol: ProtocolNetherNet,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := pcGuestPortForwards(test.mode, test.protocol, "10.144.144.1", 40002, 40004, 40001, 40003)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("pcGuestPortForwards() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestPCGuestDialAddresses(t *testing.T) {
	if got, want := pcControlDialAddr(easytier.DialModeProxy, "10.144.144.1", 40002, 40001), "127.0.0.1:40001"; got != want {
		t.Fatalf("proxy control address = %q, want %q", got, want)
	}
	if got, want := pcRakDialAddr(easytier.DialModeProxy, "10.144.144.1", 40004, 40003), "127.0.0.1:40003"; got != want {
		t.Fatalf("proxy RakNet address = %q, want %q", got, want)
	}
	if got, want := pcControlDialAddr(easytier.DialModeDirect, "10.144.144.1", 40002, 40001), "10.144.144.1:40002"; got != want {
		t.Fatalf("direct control address = %q, want %q", got, want)
	}
	if got, want := pcRakDialAddr(easytier.DialModeDirect, "10.144.144.1", 40004, 40003), "10.144.144.1:40004"; got != want {
		t.Fatalf("direct RakNet address = %q, want %q", got, want)
	}
}
