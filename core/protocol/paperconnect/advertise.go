package paperconnect

import (
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/df-mc/go-nethernet/discovery"
	"github.com/wlynxg/anet"
)

// 平台感知的本地发现策略：Android 与 Windows 的 255.255.255.255 广播
// 不回环，本机 MC 客户端的 discovery Request 到不了 fake server，房间
// 不显示。此处用 unicast 广告补偿。Windows 同样依赖，勿标为 Android 专属。

// pcAdvertiseLoop 周期性向本机所有地址(loopback、网卡 IP)单播 NetherNet
// discovery 响应，使房间出现在本机 MC 客户端的局域网列表。响应从
// listener 的 socket 发出(源端口 7551)，客户端可据此回复连接。
func (s *PaperConnectService) pcAdvertiseLoop(disc *discovery.Listener) {
	stopCh := s.guestStopCh
	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()

	addrs := []*net.UDPAddr{{
		IP:   net.IPv4(127, 0, 0, 1),
		Port: 7551,
	}}
	addrs = append(addrs, getLocalAddrs(7551)...)

	for {
		select {
		case <-stopCh:
			return
		case <-disc.Context().Done():
			return
		case <-ticker.C:
			for _, a := range addrs {
				if err := disc.Advertise(a); err != nil {
					slog.Warn("discovery advertisement failed", "to", a.String(), "err", err)
				} else {
					slog.Debug("discovery advertisement sent", "to", a.String())
				}
			}
		}
	}
}

// getLocalAddrs returns all local IPv4 unicast addresses including 127.0.0.1.
// On Windows, broadcasts to 255.255.255.255 don't loopback, so local unicast pings
// are needed to discover servers on the same machine.
func getLocalAddrs(port int) []*net.UDPAddr {
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
