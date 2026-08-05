package paperconnect

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync/atomic"
	"time"

	raknet "github.com/sandertv/go-raknet"
)

func (s *PaperConnectService) pcHostRakNetAcceptLoop(ctx context.Context) {
	var sessionID atomic.Uint64
	for {
		conn, err := s.hostRakLn.Accept()
		if err != nil {
			select {
			case <-s.hostStopCh:
				return
			default:
				slog.Error("RakNet accept error", "err", err)
				continue
			}
		}

		rkConn, ok := conn.(*raknet.Conn)
		if !ok {
			slog.Error("unexpected RakNet connection type", "type", fmt.Sprintf("%T", conn))
			_ = conn.Close()
			continue
		}

		select {
		case s.hostSessions <- struct{}{}:
			id := sessionID.Add(1)
			go func() {
				defer func() { <-s.hostSessions }()
				s.pcHostSession(ctx, slog.With("session", id), rkConn)
			}()
		case <-s.hostStopCh:
			_ = rkConn.Close()
			return
		case <-ctx.Done():
			_ = rkConn.Close()
			return
		}
	}
}

func (s *PaperConnectService) pcHostSession(ctx context.Context, log *slog.Logger, rkConn *raknet.Conn) {
	defer rkConn.Close()

	nnConn, err := dialLocalNetherNet(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Error("failed to dial local Bedrock world", "err", err)
		}
		return
	}
	defer nnConn.Close()

	proxyPackets(ctx, log, nnConn, rkConn)
}

func (s *PaperConnectService) pcHostServerLoop() {
	for {
		conn, err := s.hostTcpLn.Accept()
		if err != nil {
			select {
			case <-s.hostStopCh:
				return
			default:
				continue
			}
		}
		go s.pcHandleHostConnection(conn)
	}
}

func (s *PaperConnectService) pcHandleHostConnection(conn net.Conn) {
	defer conn.Close()

	for {
		s.hostMu.Lock()
		running := s.hostRunning
		s.hostMu.Unlock()
		if !running {
			return
		}

		namespace, rawJson, err := ReadPCRequest(conn)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			return
		}

		switch namespace {
		case PCPing:
			s.pcHandlePing(conn, rawJson)
		case PCPlayer:
			s.pcHandlePlayer(conn, rawJson)
		default:
			WritePCError(conn, fmt.Sprintf("Unknown namespace: %s", namespace))
		}
	}
}

func (s *PaperConnectService) pcHandlePing(conn net.Conn, rawJson []byte) {
	var req PCPingRequest
	if err := json.Unmarshal(rawJson, &req); err != nil {
		WritePCError(conn, "Invalid ping request")
		return
	}

	resp := PCPingResponse{
		Time:             req.Time,
		ReturnTime:       time.Now().UnixMilli(),
		GameType:         "MinecraftBedrock",
		GameProtocolType: "UDP",
		GamePort:         int(s.hostGamePort),
		Protocol:         s.hostProtocol,
	}
	WritePCResponse(conn, resp)
}

func (s *PaperConnectService) pcHandlePlayer(conn net.Conn, rawJson []byte) {
	var req PCPlayerRequest
	if err := json.Unmarshal(rawJson, &req); err != nil {
		WritePCError(conn, "Invalid player request")
		return
	}

	if req.PlayerName == "" || req.ClientId == "" {
		WritePCError(conn, "Missing playerName or clientId")
		return
	}

	isNew := false
	s.hostPlayerMu.Lock()
	if _, exists := s.hostPlayers[req.PlayerName]; !exists {
		isNew = true
	}
	s.hostPlayers[req.PlayerName] = &PCPlayerEntry{
		PlayerName:    req.PlayerName,
		ClientId:      req.ClientId,
		IsRoomHost:    false,
		lastHeartbeat: time.Now(),
	}

	activePlayers := make([]PCPlayerEntry, 0)
	for _, p := range s.hostPlayers {
		if p.IsRoomHost || time.Since(p.lastHeartbeat) <= pcPlayerTimeout {
			activePlayers = append(activePlayers, PCPlayerEntry{
				PlayerName: p.PlayerName,
				ClientId:   p.ClientId,
				IsRoomHost: p.IsRoomHost,
			})
		}
	}
	s.hostPlayerMu.Unlock()

	if isNew {
		s.eventEmitter.Emit("paperconnect.room.player_joined", PCPlayerEntry{
			PlayerName: req.PlayerName,
			ClientId:   req.ClientId,
			IsRoomHost: false,
		})
		s.eventEmitter.Emit("paperconnect.room.info", s.pcBuildRoomStatus(""))
	}

	resp := PCPlayerResponse{
		ReturnTime: time.Now().UnixMilli(),
		Players:    activePlayers,
	}
	WritePCResponse(conn, resp)
}

func (s *PaperConnectService) pcHostPlayerCleanupLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.hostPlayerMu.Lock()
			prevCount := len(s.hostPlayers)
			var leftPlayers []PCPlayerEntry
			now := time.Now()
			for name, p := range s.hostPlayers {
				if !p.IsRoomHost && now.Sub(p.lastHeartbeat) > pcPlayerTimeout {
					leftPlayers = append(leftPlayers, *p)
					delete(s.hostPlayers, name)
				}
			}
			needInfo := len(s.hostPlayers) < prevCount
			s.hostPlayerMu.Unlock()
			for _, p := range leftPlayers {
				s.eventEmitter.Emit("paperconnect.room.player_left", p)
			}
			if needInfo {
				s.eventEmitter.Emit("paperconnect.room.info", s.pcBuildRoomStatus(""))
			}
		case <-s.hostStopCh:
			return
		}
	}
}
