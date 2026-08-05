//go:build !et_ffi

package cli

import (
	"log/slog"

	"gravitycone/core/protocol/common"
	"gravitycone/core/protocol/scaffolding"
)

func (h *Handler) handleRoomCreate(req Request) {
	if err := h.applyRelayParams(req); err != nil {
		h.fail(req, ErrInvalidParams, err)
		return
	}

	playerName, err := req.getString("player_name")
	if err != nil {
		h.fail(req, ErrInvalidParams, err)
		return
	}

	protocol, _ := req.getString("protocol")

	if protocol == "paperconnect" {
		result, err := h.paperConnectSvc.CreateRoom(playerName, h.vendorPrefix)
		if err != nil {
			h.failRoom(req, err)
			return
		}
		h.writer.WriteResponse(successResponse(req.ID, map[string]any{
			"code":         result.Code,
			"game_port":    result.GamePort,
			"online_count": result.OnlineCount,
			"players":      result.Players,
			"running":      result.Running,
			"protocol":     "paperconnect",
			"sub_protocol": result.SubProtocol,
		}))
		return
	}

	// Default: Scaffolding (Java Edition)
	mcPort, err := req.getInt("mc_port")
	if err != nil {
		h.fail(req, ErrInvalidParams, err)
		return
	}

	result, err := h.scaffoldingSvc.CreateRoom(uint16(mcPort), playerName, h.vendorPrefix, h.motd)
	if err != nil {
		h.failRoom(req, err)
		return
	}
	h.writer.WriteResponse(successResponse(req.ID, result))
}

func (h *Handler) handleRoomStop(req Request) {
	// Try PaperConnect first, then Scaffolding
	if err := h.paperConnectSvc.StopRoom(); err == nil {
		h.ok(req)
		return
	}
	if err := h.scaffoldingSvc.StopRoom(); err != nil {
		h.failRoom(req, err)
		return
	}
	h.ok(req)
}

func (h *Handler) handleRoomJoin(req Request) {
	if err := h.applyRelayParams(req); err != nil {
		h.fail(req, ErrInvalidParams, err)
		return
	}

	code, err := req.getString("code")
	if err != nil {
		h.fail(req, ErrInvalidParams, err)
		return
	}
	playerName, err := req.getString("player_name")
	if err != nil {
		h.fail(req, ErrInvalidParams, err)
		return
	}

	if common.IsPaperConnectCode(code) {
		result, err := h.paperConnectSvc.JoinRoom(code, playerName, h.vendorPrefix, h.motd)
		if err != nil {
			h.failRoom(req, err)
			return
		}
		h.writer.WriteResponse(successResponse(req.ID, map[string]any{
			"room_code":         result.RoomCode,
			"host_address":      result.HostAddress,
			"game_port":         result.GamePort,
			"connected":         result.Connected,
			"online_count":      result.OnlineCount,
			"players":           result.Players,
			"heartbeating":      result.Heartbeating,
			"disconnect_reason": result.DisconnectReason,
			"protocol":          "paperconnect",
			"sub_protocol":      result.SubProtocol,
		}))
		return
	}

	// Scaffolding (U/) join with progress callback
	scaffolding.SetScaffoldingJoinProgress(h.scaffoldingSvc, func(step string) {
		h.writer.WriteResponse(progressResponse(req.ID, map[string]string{
			"step":    step,
			"message": progressMessage(step),
		}))
	})
	defer scaffolding.SetScaffoldingJoinProgress(h.scaffoldingSvc, nil)

	result, err := h.scaffoldingSvc.JoinRoom(code, playerName, h.vendorPrefix, h.motd)
	if err != nil {
		h.failRoom(req, err)
		return
	}
	h.writer.WriteResponse(successResponse(req.ID, result))
}

func (h *Handler) handleRoomLeave(req Request) {
	// Try PaperConnect first, then Scaffolding
	if err := h.paperConnectSvc.LeaveRoom(); err == nil {
		h.ok(req)
		return
	}
	if err := h.scaffoldingSvc.LeaveRoom(); err != nil {
		h.failRoom(req, err)
		return
	}
	h.ok(req)
}

func (h *Handler) handleRoomStatus(req Request) {
	// Try PaperConnect host status first
	pcHostStatus, pcHostErr := h.paperConnectSvc.GetRoomStatus()
	if pcHostErr == nil {
		result := hostStatusResult(pcHostStatus.Code, pcHostStatus.OnlineCount, pcHostStatus.Players, pcHostStatus.Running)
		result["game_port"] = pcHostStatus.GamePort
		result["protocol"] = "paperconnect"
		result["sub_protocol"] = pcHostStatus.SubProtocol
		h.writer.WriteResponse(successResponse(req.ID, result))
		return
	}
	slog.Debug("paperconnect host status unavailable", "error", pcHostErr)

	// Try Scaffolding host status
	hostStatus, hostErr := h.scaffoldingSvc.GetRoomStatus()
	if hostErr == nil {
		result := hostStatusResult(hostStatus.Code, hostStatus.OnlineCount, hostStatus.Players, hostStatus.Running)
		result["mc_address"] = hostStatus.MCAddress
		result["mc_port"] = hostStatus.MCPort
		h.writer.WriteResponse(successResponse(req.ID, result))
		return
	}
	slog.Debug("scaffolding host status unavailable", "error", hostErr)

	// Try PaperConnect guest status
	pcGuestStatus, pcGuestErr := h.paperConnectSvc.GetConnectionStatus()
	if pcGuestErr == nil {
		result := guestStatusResult(pcGuestStatus.RoomCode, pcGuestStatus.HostAddress, pcGuestStatus.Connected, pcGuestStatus.OnlineCount, pcGuestStatus.Players, pcGuestStatus.Heartbeating, pcGuestStatus.DisconnectReason)
		result["game_port"] = pcGuestStatus.GamePort
		result["protocol"] = "paperconnect"
		result["sub_protocol"] = pcGuestStatus.SubProtocol
		h.writer.WriteResponse(successResponse(req.ID, result))
		return
	}
	slog.Debug("paperconnect guest status unavailable", "error", pcGuestErr)

	// Try Scaffolding guest status
	guestStatus, guestErr := h.scaffoldingSvc.GetConnectionStatus()
	if guestErr == nil {
		result := guestStatusResult(guestStatus.RoomCode, guestStatus.HostAddress, guestStatus.Connected, guestStatus.OnlineCount, guestStatus.Players, guestStatus.Heartbeating, guestStatus.DisconnectReason)
		result["mc_address"] = guestStatus.MCAddress
		result["mc_port"] = guestStatus.MCPort
		h.writer.WriteResponse(successResponse(req.ID, result))
		return
	}
	slog.Debug("scaffolding guest status unavailable", "error", guestErr)

	h.writer.WriteResponse(successResponse(req.ID, map[string]string{"role": "none"}))
}

func hostStatusResult(code string, onlineCount int, players any, running bool) map[string]any {
	return map[string]any{
		"role":         "host",
		"code":         code,
		"online_count": onlineCount,
		"players":      players,
		"running":      running,
	}
}

func guestStatusResult(roomCode, hostAddress string, connected bool, onlineCount int, players any, heartbeating bool, disconnectReason string) map[string]any {
	return map[string]any{
		"role":              "guest",
		"room_code":         roomCode,
		"host_address":      hostAddress,
		"connected":         connected,
		"online_count":      onlineCount,
		"players":           players,
		"heartbeating":      heartbeating,
		"disconnect_reason": disconnectReason,
	}
}
