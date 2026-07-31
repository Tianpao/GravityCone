package discovery

import (
	"syscall"
)

func reuseAddrControl(_ string, _ string, c syscall.RawConn) error {
	var controlErr error
	if err := c.Control(func(fd uintptr) {
		controlErr = setReuseAddr(fd)
	}); err != nil {
		return err
	}
	return controlErr
}
