//go:build !et_ffi

package easytier

import (
	"fmt"
	"time"
)

func (m *EasyTierManager) AddPortForward(proto string, localAddr string, remoteAddr string) error {
	return m.runPortForwardCmd("add", proto, localAddr, remoteAddr, "添加端口转发失败")
}

func (m *EasyTierManager) RemovePortForward(proto string, localAddr string, remoteAddr string) error {
	return m.runPortForwardCmd("remove", proto, localAddr, remoteAddr, "删除端口转发失败")
}

func (m *EasyTierManager) runPortForwardCmd(action, proto, localAddr, remoteAddr, errMsg string) error {
	rpcPortal := m.RPCPortal()
	if rpcPortal == "" {
		return fmt.Errorf("easytier-core 未运行，无法%s", errMsg)
	}
	var lastErr error
	var lastOut string
	for attempt := 0; attempt < 3; attempt++ {
		out, err := m.runCli(
			"-p", rpcPortal,
			"port-forward", action,
			proto, localAddr, remoteAddr,
		)
		if err == nil {
			return nil
		}
		lastErr, lastOut = err, out
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * time.Second)
		}
	}
	return fmt.Errorf("%s (%s %s -> %s): %w, output: %s", errMsg, proto, localAddr, remoteAddr, lastErr, lastOut)
}
