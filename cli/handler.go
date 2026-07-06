package cli

import (
	"fmt"
	"gravitycone/core"
	"strings"
	"sync"
)

// Handler dispatches CLI requests to core service methods.
type Handler struct {
	stunSvc        *core.StunService
	lanSvc         *core.LanService
	scaffoldingSvc *core.ScaffoldingService
	writer         *StdioWriter
	shutdownCh     chan struct{}
	shutdownOnce   sync.Once
	vendorPrefix   string
}

// NewHandler creates a Handler with the given services and writer.
func NewHandler(
	stunSvc *core.StunService,
	lanSvc *core.LanService,
	scaffoldingSvc *core.ScaffoldingService,
	writer *StdioWriter,
	shutdownCh chan struct{},
	vendorPrefix string,
) *Handler {
	return &Handler{
		stunSvc:        stunSvc,
		lanSvc:         lanSvc,
		scaffoldingSvc: scaffoldingSvc,
		writer:         writer,
		shutdownCh:     shutdownCh,
		vendorPrefix:   vendorPrefix,
	}
}

// Handle processes a single request and writes the response.
func (h *Handler) Handle(req Request) {
	parts := strings.SplitN(req.Method, ".", 2)
	if len(parts) != 2 {
		h.writer.WriteResponse(errorResponse(req.ID, ErrInvalidMethod, req.Method))
		return
	}
	group, action := parts[0], parts[1]

	switch group {
	case "stun":
		h.handleStun(req, action)
	case "room":
		h.handleRoom(req, action)
	case "lan":
		h.handleLan(req, action)
	case "system":
		h.handleSystem(req, action)
	default:
		h.writer.WriteResponse(errorResponse(req.ID, ErrInvalidMethod, req.Method))
	}
}

func (h *Handler) handleStun(req Request, action string) {
	switch action {
	case "probe":
		result, err := h.stunSvc.TestStun()
		if err != nil {
			h.writer.WriteResponse(errorResponse(req.ID, mapStunError(err), err.Error()))
			return
		}
		h.writer.WriteResponse(successResponse(req.ID, result))
	default:
		h.writer.WriteResponse(errorResponse(req.ID, ErrInvalidMethod, req.Method))
	}
}

func (h *Handler) handleRoom(req Request, action string) {
	switch action {
	case "create":
		mcPort, err := req.getInt("mc_port")
		if err != nil {
			h.writer.WriteResponse(errorResponse(req.ID, ErrInvalidParams, err.Error()))
			return
		}
		playerName, err := req.getString("player_name")
		if err != nil {
			h.writer.WriteResponse(errorResponse(req.ID, ErrInvalidParams, err.Error()))
			return
		}

		result, err := h.scaffoldingSvc.CreateRoom(uint16(mcPort), playerName, h.vendorPrefix)
		if err != nil {
			h.writer.WriteResponse(errorResponse(req.ID, mapRoomError(err), err.Error()))
			return
		}
		h.writer.WriteResponse(successResponse(req.ID, result))

	case "stop":
		err := h.scaffoldingSvc.StopRoom()
		if err != nil {
			h.writer.WriteResponse(errorResponse(req.ID, mapRoomError(err), err.Error()))
			return
		}
		h.writer.WriteResponse(successResponse(req.ID, map[string]interface{}{}))

	case "join":
		code, err := req.getString("code")
		if err != nil {
			h.writer.WriteResponse(errorResponse(req.ID, ErrInvalidParams, err.Error()))
			return
		}
		playerName, err := req.getString("player_name")
		if err != nil {
			h.writer.WriteResponse(errorResponse(req.ID, ErrInvalidParams, err.Error()))
			return
		}

		// Set progress callback that writes progress responses
		core.SetScaffoldingJoinProgress(h.scaffoldingSvc, func(step string) {
			h.writer.WriteResponse(progressResponse(req.ID, map[string]string{
				"step":    step,
				"message": progressMessage(step),
			}))
		})
		defer core.SetScaffoldingJoinProgress(h.scaffoldingSvc, nil)

		result, err := h.scaffoldingSvc.JoinRoom(code, playerName, h.vendorPrefix)
		if err != nil {
			h.writer.WriteResponse(errorResponse(req.ID, mapRoomError(err), err.Error()))
			return
		}
		h.writer.WriteResponse(successResponse(req.ID, result))

	case "cancel_join":
		h.scaffoldingSvc.CancelJoin()
		h.writer.WriteResponse(successResponse(req.ID, map[string]interface{}{}))

	case "leave":
		err := h.scaffoldingSvc.LeaveRoom()
		if err != nil {
			h.writer.WriteResponse(errorResponse(req.ID, mapRoomError(err), err.Error()))
			return
		}
		h.writer.WriteResponse(successResponse(req.ID, map[string]interface{}{}))

	case "status":
		h.handleRoomStatus(req)

	default:
		h.writer.WriteResponse(errorResponse(req.ID, ErrInvalidMethod, req.Method))
	}
}

func (h *Handler) handleRoomStatus(req Request) {
	// Try host status first
	hostStatus, hostErr := h.scaffoldingSvc.GetRoomStatus()
	if hostErr == nil {
		h.writer.WriteResponse(successResponse(req.ID, map[string]interface{}{
			"role":  "host",
			"code":  hostStatus.Code,
			"mc_address": hostStatus.MCAddress,
			"mc_port":    hostStatus.MCPort,
			"online_count": hostStatus.OnlineCount,
			"players":     hostStatus.Players,
			"running":     hostStatus.Running,
		}))
		return
	}

	// Try guest status
	guestStatus, guestErr := h.scaffoldingSvc.GetConnectionStatus()
	if guestErr == nil {
		h.writer.WriteResponse(successResponse(req.ID, map[string]interface{}{
			"role":              "guest",
			"room_code":         guestStatus.RoomCode,
			"host_address":      guestStatus.HostAddress,
			"mc_address":        guestStatus.MCAddress,
			"mc_port":           guestStatus.MCPort,
			"connected":         guestStatus.Connected,
			"online_count":      guestStatus.OnlineCount,
			"players":           guestStatus.Players,
			"heartbeating":      guestStatus.Heartbeating,
			"disconnect_reason": guestStatus.DisconnectReason,
		}))
		return
	}

	// Not in any room
	h.writer.WriteResponse(successResponse(req.ID, map[string]string{"role": "none"}))
}

func (h *Handler) handleLan(req Request, action string) {
	switch action {
	case "start_discovery":
		err := h.lanSvc.StartDiscovery()
		if err != nil {
			h.writer.WriteResponse(errorResponse(req.ID, ErrInternalError, err.Error()))
			return
		}
		h.writer.WriteResponse(successResponse(req.ID, map[string]interface{}{}))

	case "stop_discovery":
		h.lanSvc.StopDiscovery()
		h.writer.WriteResponse(successResponse(req.ID, map[string]interface{}{}))

	case "list_servers":
		servers := h.lanSvc.GetDiscoveredServers()
		h.writer.WriteResponse(successResponse(req.ID, map[string]interface{}{
			"servers": servers,
		}))

	case "verify_server":
		ip, err := req.getString("ip")
		if err != nil {
			h.writer.WriteResponse(errorResponse(req.ID, ErrInvalidParams, err.Error()))
			return
		}
		port, err := req.getInt("port")
		if err != nil {
			h.writer.WriteResponse(errorResponse(req.ID, ErrInvalidParams, err.Error()))
			return
		}
		version, err := h.lanSvc.VerifyServer(ip, port)
		if err != nil {
			h.writer.WriteResponse(errorResponse(req.ID, ErrInternalError, err.Error()))
			return
		}
		h.writer.WriteResponse(successResponse(req.ID, map[string]interface{}{
			"online":  true,
			"version": version,
		}))

	default:
		h.writer.WriteResponse(errorResponse(req.ID, ErrInvalidMethod, req.Method))
	}
}

func (h *Handler) handleSystem(req Request, action string) {
	switch action {
	case "ping":
		h.writer.WriteResponse(successResponse(req.ID, map[string]bool{"pong": true}))
	case "shutdown":
		h.writer.WriteResponse(successResponse(req.ID, map[string]interface{}{}))
		h.shutdownOnce.Do(func() {
			close(h.shutdownCh)
		})
	default:
		h.writer.WriteResponse(errorResponse(req.ID, ErrInvalidMethod, req.Method))
	}
}

// mapStunError maps a STUN-related error to a CLI error code.
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

// mapRoomError maps a room-related error to a CLI error code.
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

// progressMessage returns a human-readable message for a join progress step.
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

// --- Request parameter helpers ---

func (r *Request) getString(key string) (string, error) {
	v, ok := r.Params[key]
	if !ok {
		return "", fmt.Errorf("missing required parameter: %s", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("parameter %s must be a string", key)
	}
	return s, nil
}

func (r *Request) getInt(key string) (int, error) {
	v, ok := r.Params[key]
	if !ok {
		return 0, fmt.Errorf("missing required parameter: %s", key)
	}
	// JSON numbers are float64 in Go's default unmarshal
	switch n := v.(type) {
	case float64:
		return int(n), nil
	case int:
		return n, nil
	default:
		return 0, fmt.Errorf("parameter %s must be a number", key)
	}
}
