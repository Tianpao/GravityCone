package cli

import (
	"fmt"
	"gravitycone/core/easytier"
	"gravitycone/core/minecraft"
	"gravitycone/core/protocol/scaffolding"
	"strings"
	"sync"
)

// Handler dispatches CLI requests to core service methods.
type Handler struct {
	stunSvc        *easytier.StunService
	lanSvc         *minecraft.LanService
	scaffoldingSvc *scaffolding.ScaffoldingService
	writer         *StdioWriter
	shutdownCh     chan struct{}
	shutdownOnce   sync.Once
	vendorPrefix   string
	motd           string
}

// NewHandler creates a Handler with the given services and writer.
func NewHandler(
	stunSvc *easytier.StunService,
	lanSvc *minecraft.LanService,
	scaffoldingSvc *scaffolding.ScaffoldingService,
	writer *StdioWriter,
	shutdownCh chan struct{},
	vendorPrefix string,
	motd string,
) *Handler {
	return &Handler{
		stunSvc:        stunSvc,
		lanSvc:         lanSvc,
		scaffoldingSvc: scaffoldingSvc,
		writer:         writer,
		shutdownCh:     shutdownCh,
		vendorPrefix:   vendorPrefix,
		motd:           motd,
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

		result, err := h.scaffoldingSvc.CreateRoom(uint16(mcPort), playerName, h.vendorPrefix, h.motd)
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
		scaffolding.SetScaffoldingJoinProgress(h.scaffoldingSvc, func(step string) {
			h.writer.WriteResponse(progressResponse(req.ID, map[string]string{
				"step":    step,
				"message": progressMessage(step),
			}))
		})
		defer scaffolding.SetScaffoldingJoinProgress(h.scaffoldingSvc, nil)

		result, err := h.scaffoldingSvc.JoinRoom(code, playerName, h.vendorPrefix, h.motd)
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
	if status, err := h.scaffoldingSvc.GetRoomStatus(); err == nil {
		h.writer.WriteResponse(successResponse(req.ID, map[string]interface{}{
			"role":         "host",
			"code":         status.Code,
			"mc_address":   status.MCAddress,
			"mc_port":      status.MCPort,
			"online_count": status.OnlineCount,
			"players":      status.Players,
			"running":      status.Running,
		}))
		return
	}

	if status, err := h.scaffoldingSvc.GetConnectionStatus(); err == nil {
		h.writer.WriteResponse(successResponse(req.ID, map[string]interface{}{
			"role":              "guest",
			"room_code":         status.RoomCode,
			"host_address":      status.HostAddress,
			"mc_address":        status.MCAddress,
			"mc_port":           status.MCPort,
			"connected":         status.Connected,
			"online_count":      status.OnlineCount,
			"players":           status.Players,
			"heartbeating":      status.Heartbeating,
			"disconnect_reason": status.DisconnectReason,
		}))
		return
	}

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
	case "add_peers":
		h.handleAddPeers(req)
	default:
		h.writer.WriteResponse(errorResponse(req.ID, ErrInvalidMethod, req.Method))
	}
}

func (h *Handler) handleAddPeers(req Request) {
	rawPeers, ok := req.Params["peers"]
	if !ok {
		h.writer.WriteResponse(errorResponse(req.ID, ErrInvalidParams, "missing required parameter: peers"))
		return
	}
	peersArr, ok := rawPeers.([]interface{})
	if !ok {
		h.writer.WriteResponse(errorResponse(req.ID, ErrInvalidParams, "parameter peers must be an array of strings"))
		return
	}
	var addrs []string
	for _, v := range peersArr {
		s, ok := v.(string)
		if !ok {
			h.writer.WriteResponse(errorResponse(req.ID, ErrInvalidParams, "parameter peers must be an array of strings"))
			return
		}
		if s != "" {
			addrs = append(addrs, s)
		}
	}
	if len(addrs) == 0 {
		h.writer.WriteResponse(errorResponse(req.ID, ErrInvalidParams, "peers array must not be empty"))
		return
	}
	h.scaffoldingSvc.AddPeers(addrs)
	h.writer.WriteResponse(successResponse(req.ID, map[string]interface{}{}))
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
