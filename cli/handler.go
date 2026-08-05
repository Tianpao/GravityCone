//go:build !et_ffi

package cli

import (
	"fmt"
	"gravitycone/core/easytier"
	"gravitycone/core/lan"
	"gravitycone/core/protocol/paperconnect"
	"gravitycone/core/protocol/scaffolding"
	"strings"
	"sync"
)

// Handler dispatches CLI requests to core service methods.
type Handler struct {
	stunSvc         *easytier.StunService
	lanSvc          *lan.LanService
	scaffoldingSvc  *scaffolding.ScaffoldingService
	paperConnectSvc *paperconnect.PaperConnectService
	writer          *StdioWriter
	shutdownCh      chan struct{}
	shutdownOnce    sync.Once
	vendorPrefix    string
	motd            string
}

// NewHandler creates a Handler with the given services and writer.
func NewHandler(
	stunSvc *easytier.StunService,
	lanSvc *lan.LanService,
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
		h.handleRoomCreate(req)

	case "stop":
		h.handleRoomStop(req)

	case "join":
		h.handleRoomJoin(req)

	case "cancel_join":
		// Cancel both — whichever is active will respond
		h.scaffoldingSvc.CancelJoin()
		h.paperConnectSvc.CancelJoin()
		h.writer.WriteResponse(successResponse(req.ID, map[string]interface{}{}))

	case "confirm_minecraft_ended":
		if err := h.paperConnectSvc.ConfirmMinecraftEnded(); err != nil {
			h.writer.WriteResponse(errorResponse(req.ID, ErrInternalError, err.Error()))
			return
		}
		h.writer.WriteResponse(successResponse(req.ID, map[string]interface{}{}))

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
		relayObj, ok := raw.(map[string]interface{})
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
