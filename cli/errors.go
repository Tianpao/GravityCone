//go:build !et_ffi

package cli

import "strings"

func mapStunError(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "easytier-cli") {
		return ErrSTUNFailed
	}
	if strings.Contains(msg, "parse") {
		return ErrSTUNParseError
	}
	if strings.Contains(msg, "not found") {
		return ErrEasytierNotFound
	}
	return ErrInternalError
}

func mapRoomError(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "已有房间在运行") {
		return ErrRoomAlreadyRun
	}
	if strings.Contains(msg, "已在一个房间中") {
		return ErrRoomAlreadyRun
	}
	if strings.Contains(msg, "未找到") || strings.Contains(msg, "房间代码") {
		return ErrRoomNotFound
	}
	if strings.Contains(msg, "未连接") {
		return ErrNotConnected
	}
	return ErrInternalError
}

func progressMessage(step string) string {
	switch step {
	case "resolving":
		return "正在解析房间代码"
	case "connecting":
		return "正在连接 EasyTier 网络..."
	case "waiting_peer":
		return "等待对端节点上线..."
	case "handshaking":
		return "正在握手协商..."
	case "ready":
		return "连接就绪"
	default:
		return step
	}
}
