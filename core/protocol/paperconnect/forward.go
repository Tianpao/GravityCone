package paperconnect

import (
	"fmt"

	"gravitycone/core/easytier"
)

// pcUDPBindHost 返回房客 UDP 转发/监听的绑定地址。NetherNet 只允许本机
// 连接，绑回环；RakNet 需 0.0.0.0（Pong 不携带 IP，客户端把来源 IP 当作
// 房间 IP）。套接字与转发串必须使用同一地址，否则转发流量到不了套接字。
func pcUDPBindHost(protocol string) string {
	if protocol == ProtocolNetherNet {
		return "127.0.0.1"
	}
	return "0.0.0.0"
}

func pcGuestPortForwards(mode easytier.DialMode, protocol, hostIP string, serverPort, gamePort, tcpLocalPort, rakLocalPort uint16) []string {
	if mode != easytier.DialModeProxy {
		return nil
	}

	return []string{
		fmt.Sprintf("tcp://127.0.0.1:%d/%s:%d", tcpLocalPort, hostIP, serverPort),
		fmt.Sprintf("udp://%s:%d/%s:%d", pcUDPBindHost(protocol), rakLocalPort, hostIP, gamePort),
	}
}

// pcDialAddr 返回通道的拨号地址：proxy 模式经本地端口转发走回环地址，
// direct 模式（TUN）直连 host 虚拟 IP 的远端端口。
func pcDialAddr(mode easytier.DialMode, hostIP string, remotePort, localPort uint16) string {
	if mode == easytier.DialModeProxy {
		return fmt.Sprintf("127.0.0.1:%d", localPort)
	}
	return fmt.Sprintf("%s:%d", hostIP, remotePort)
}
