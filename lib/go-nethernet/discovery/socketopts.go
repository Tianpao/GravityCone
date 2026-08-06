package discovery

import (
	"syscall"
)

// reuseAddrControl 允许 fake server 与 MC 客户端共享绑定 UDP 7551
// （Windows 广播不回环、Android VPN 路由均需要）。Linux 上 UDP 的
// SO_REUSEADDR 无效，需 SO_REUSEPORT（见 socketopts_unix.go）。
func reuseAddrControl(_ string, _ string, c syscall.RawConn) error {
	var controlErr error
	if err := c.Control(func(fd uintptr) {
		controlErr = setReuseAddr(fd)
	}); err != nil {
		return err
	}
	return controlErr
}
