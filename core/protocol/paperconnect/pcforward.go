package paperconnect

import (
	"fmt"

	"gravitycone/core/easytier"
)

func pcGuestPortForwards(mode easytier.DialMode, protocol, hostIP string, serverPort, gamePort, tcpLocalPort, rakLocalPort uint16) []string {
	if mode != easytier.DialModeProxy {
		return nil
	}

	udpBindHost := "0.0.0.0"
	if protocol == ProtocolNetherNet {
		udpBindHost = "127.0.0.1"
	}
	return []string{
		fmt.Sprintf("tcp://127.0.0.1:%d/%s:%d", tcpLocalPort, hostIP, serverPort),
		fmt.Sprintf("udp://%s:%d/%s:%d", udpBindHost, rakLocalPort, hostIP, gamePort),
	}
}

func pcControlDialAddr(mode easytier.DialMode, hostIP string, serverPort, tcpLocalPort uint16) string {
	if mode == easytier.DialModeProxy {
		return fmt.Sprintf("127.0.0.1:%d", tcpLocalPort)
	}
	return fmt.Sprintf("%s:%d", hostIP, serverPort)
}

func pcRakDialAddr(mode easytier.DialMode, hostIP string, gamePort, rakLocalPort uint16) string {
	if mode == easytier.DialModeProxy {
		return fmt.Sprintf("127.0.0.1:%d", rakLocalPort)
	}
	return fmt.Sprintf("%s:%d", hostIP, gamePort)
}
