package paperconnect

import (
	"fmt"
	"net"

	"github.com/wlynxg/anet"
)

// GetLocalAddrs returns all local IPv4 unicast addresses including 127.0.0.1.
// On Windows, broadcasts to 255.255.255.255 don't loopback, so local unicast pings
// are needed to discover servers on the same machine.
func GetLocalAddrs(port int) []*net.UDPAddr {
	interfaces, err := anet.Interfaces()
	if err != nil {
		return nil
	}
	var addrs []*net.UDPAddr
	for _, iface := range interfaces {
		if !isPhysicalNIC(iface) {
			continue
		}
		ifaceAddrs, err := anet.InterfaceAddrsByInterface(&iface)
		if err != nil {
			continue
		}
		for _, a := range ifaceAddrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipNet.IP.To4()
			if ip4 == nil || isEasyTierIP(ip4) {
				continue
			}
			udpAddr, _ := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", ip4.String(), port))
			if udpAddr != nil {
				addrs = append(addrs, udpAddr)
			}
		}
	}
	// Windows 上发往物理网卡 IP 的 unicast 不一定回环，必须额外带上 127.0.0.1。
	if udpAddr, _ := net.ResolveUDPAddr("udp4", fmt.Sprintf("127.0.0.1:%d", port)); udpAddr != nil {
		addrs = append(addrs, udpAddr)
	}
	return addrs
}
