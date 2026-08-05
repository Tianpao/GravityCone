package paperconnect

import (
	"log/slog"
	"net"
	"time"

	"github.com/df-mc/go-nethernet/discovery"

	lanpc "gravitycone/core/lan/paperconnect"
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
	addrs = append(addrs, lanpc.GetLocalAddrs(7551)...)

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
