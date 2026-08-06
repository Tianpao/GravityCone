//go:build !et_ffi

package cli

import (
	"fmt"
	"gravitycone/core/easytier"
	lansca "gravitycone/core/lan/scaffolding"
	"gravitycone/core/protocol/paperconnect"
	"gravitycone/core/protocol/scaffolding"
	"strings"
	"sync"
)

type Handler struct {
	stunSvc         *easytier.StunService
	lanSvc          *lansca.LanService
	scaffoldingSvc  *scaffolding.ScaffoldingService
	paperConnectSvc *paperconnect.PaperConnectService
	writer          *StdioWriter
	shutdownCh      chan struct{}
	shutdownOnce    sync.Once
	vendorPrefix    string
	motd            string
}

func NewHandler(
	stunSvc *easytier.StunService,
	lanSvc *lansca.LanService,
	scaffoldingSvc *scaffolding.ScaffoldingService,
	paperConnectSvc *paperconnect.PaperConnectService,
	writer *StdioWriter,
	shutdownCh chan struct{},
	vendorPrefix string,
	motd string,
) *Handler {
	return &Handler{
		stunSvc:         stunSvc,
		lanSvc:          lanSvc,
		scaffoldingSvc:  scaffoldingSvc,
		paperConnectSvc: paperConnectSvc,
		writer:          writer,
		shutdownCh:      shutdownCh,
		vendorPrefix:    vendorPrefix,
		motd:            motd,
	}
}

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
			h.failStun(req, err)
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
		h.handleRoomCreate(req)

	case "stop":
		h.handleRoomStop(req)

	case "join":
		h.handleRoomJoin(req)

	case "cancel_join":
		// Cancel both — whichever is active will respond
		h.scaffoldingSvc.CancelJoin()
		h.paperConnectSvc.CancelJoin()
		h.ok(req)

	case "confirm_minecraft_ended":
		if err := h.paperConnectSvc.ConfirmMinecraftEnded(); err != nil {
			h.fail(req, ErrInternalError, err)
			return
		}
		h.ok(req)

	case "leave":
		h.handleRoomLeave(req)

	case "status":
		h.handleRoomStatus(req)

	default:
		h.writer.WriteResponse(errorResponse(req.ID, ErrInvalidMethod, req.Method))
	}
}

// applyRelayParams reads the optional relay object from a request and
// injects it into both protocol services. Format:
//
//	"relay": {"node_id": 123, "url": "tcp://1.2.3.4:5678"}
//
// node_id is embedded into the room code on the host side (0 = self-managed
// relay, 805 = no public nodes; default 0 when omitted); url is used
// directly as an EasyTier peer on both sides. When relay is absent or url
// is empty the external relay is cleared, reverting to built-in peers only
// (pure P2P).
func (h *Handler) applyRelayParams(req Request) error {
	relayID, relayURL := 0, ""

	if raw, ok := req.Params["relay"]; ok {
		relayObj, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("parameter relay must be an object with node_id and url")
		}
		if v, ok := relayObj["url"]; ok {
			s, ok := v.(string)
			if !ok {
				return fmt.Errorf("relay.url must be a string")
			}
			relayURL = s
		}
		// url 为空视为未设置中继（纯 P2P），node_id 无需校验
		if relayURL != "" {
			if v, ok := relayObj["node_id"]; ok {
				n, ok := toInt(v)
				if !ok {
					return fmt.Errorf("relay.node_id must be a number")
				}
				relayID = n
			}
		}
	}

	scaffolding.ConfigureExternalRelay(h.scaffoldingSvc, relayID, relayURL)
	paperconnect.ConfigureExternalRelay(h.paperConnectSvc, relayID, relayURL)
	return nil
}

func (h *Handler) handleLan(req Request, action string) {
	switch action {
	case "start_discovery":
		err := h.lanSvc.StartDiscovery()
		if err != nil {
			h.fail(req, ErrInternalError, err)
			return
		}
		h.ok(req)

	case "stop_discovery":
		h.lanSvc.StopDiscovery()
		h.ok(req)

	case "list_servers":
		servers := h.lanSvc.GetDiscoveredServers()
		h.writer.WriteResponse(successResponse(req.ID, map[string]any{
			"servers": servers,
		}))

	case "verify_server":
		ip, err := req.getString("ip")
		if err != nil {
			h.fail(req, ErrInvalidParams, err)
			return
		}
		port, err := req.getInt("port")
		if err != nil {
			h.fail(req, ErrInvalidParams, err)
			return
		}
		version, err := h.lanSvc.VerifyServer(ip, port)
		if err != nil {
			h.fail(req, ErrInternalError, err)
			return
		}
		h.writer.WriteResponse(successResponse(req.ID, map[string]any{
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
		h.ok(req)
		h.shutdownOnce.Do(func() {
			close(h.shutdownCh)
		})
	case "add_peers":
		h.handleAddPeers(req)
	default:
		h.writer.WriteResponse(errorResponse(req.ID, ErrInvalidMethod, req.Method))
	}
}

// fail 写出 error 响应（所有调用点均已保证 err 非空）。
func (h *Handler) fail(req Request, code string, err error) {
	h.writer.WriteResponse(errorResponse(req.ID, code, err.Error()))
}

// failStun 以 mapStunError 映射错误码后写出响应。
func (h *Handler) failStun(req Request, err error) { h.fail(req, mapStunError(err), err) }

// failRoom 以 mapRoomError 映射错误码后写出响应。
func (h *Handler) failRoom(req Request, err error) { h.fail(req, mapRoomError(err), err) }

// ok 写出空数据 success 响应。
func (h *Handler) ok(req Request) {
	h.writer.WriteResponse(successResponse(req.ID, map[string]any{}))
}
