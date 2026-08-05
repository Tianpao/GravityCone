package scaffolding

import (
	"encoding/binary"
	"encoding/json"
	"log/slog"
	"net"
	"strings"
	"time"

	mcstatus "github.com/andre-carbajal/go-mcstatus"
)

const guestTimeout = 15 * time.Second // 3 missed heartbeats (5s each)

func (s *ScaffoldingService) hostPlayerCleanupLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.hostPlayerMu.Lock()
			now := time.Now()
			for id, e := range s.hostPlayers {
				if e.info.Kind == KindGuest && now.Sub(e.lastSeen) > guestTimeout {
					s.eventEmitter.Emit("room.player_left", *e.info)
					delete(s.hostPlayers, id)
				}
			}
			s.hostPlayerMu.Unlock()
		case <-s.hostStopCh:
			return
		}
	}
}

func (s *ScaffoldingService) hostMCHealthCheckLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.hostMu.Lock()
			mcPort := s.mcPort
			running := s.hostRunning
			s.hostMu.Unlock()

			if !running {
				return
			}

			server := mcstatus.JavaServer{Host: "127.0.0.1", Port: mcPort}
			if _, err := server.Status(); err != nil {
				slog.Warn("MC server not responding, stopping room", "port", mcPort)
				s.hostMu.Lock()
				s.hostStopReason = "Minecraft 服务器已关闭，房间已自动销毁"
				s.hostMu.Unlock()
				s.StopRoom()
				return
			}
		case <-s.hostStopCh:
			return
		}
	}
}

func (s *ScaffoldingService) hostServerLoop() {
	for {
		conn, err := s.hostListener.Accept()
		if err != nil {
			select {
			case <-s.hostStopCh:
				return
			default:
				slog.Warn("Accept error", "error", err)
				continue
			}
		}
		slog.Info("accepted connection", "remote", conn.RemoteAddr())
		go s.handleHostConnection(conn)
	}
}

func (s *ScaffoldingService) handleHostConnection(conn net.Conn) {
	// Register connection so StopRoom can close it.
	s.hostConnMu.Lock()
	if s.hostConns != nil {
		s.hostConns[conn] = struct{}{}
		slog.Info("HostConn registered", "total", len(s.hostConns))
	} else {
		slog.Warn("HostConn not registered, hostConns is nil")
	}
	s.hostConnMu.Unlock()

	defer func() {
		conn.Close()
		s.hostConnMu.Lock()
		if s.hostConns != nil {
			delete(s.hostConns, conn)
		}
		s.hostConnMu.Unlock()
	}()

	for {
		// Check if room is still running before blocking on read.
		s.hostMu.Lock()
		running := s.hostRunning
		s.hostMu.Unlock()
		if !running {
			return
		}

		typeName, body, err := ReadProtocolRequest(conn)
		if err != nil {
			slog.Warn("ReadProtocolRequest error", "error", err)
			return
		}

		switch typeName {
		case ProtocolPing:
			s.handlePing(conn, body)
		case ProtocolProtocols:
			s.handleProtocols(conn, body)
		case ProtocolServerPort:
			s.handleServerPort(conn)
		case ProtocolPlayerPing:
			s.handlePlayerPing(conn, body)
		case ProtocolPlayerProfilesList:
			s.handlePlayerProfilesList(conn)
		default:
			WriteProtocolResponse(conn, StatusUnknownProtocol, nil)
		}
	}
}

func (s *ScaffoldingService) handlePing(conn net.Conn, body []byte) {
	if len(body) > 32 {
		WriteProtocolResponse(conn, StatusBadRequest, nil)
		return
	}
	WriteProtocolResponse(conn, StatusOK, body)
}

func (s *ScaffoldingService) handleProtocols(conn net.Conn, body []byte) {
	clientProtocols := strings.Split(string(body), "\x00")
	clientSet := make(map[string]bool, len(clientProtocols))
	for _, p := range clientProtocols {
		p = strings.TrimSpace(p)
		if p != "" {
			clientSet[p] = true
		}
	}

	var common []string
	for _, p := range serverProtocols {
		if clientSet[p] {
			common = append(common, p)
		}
	}
	if clientSet[ProtocolPlayerEasyTierID] {
		common = append(common, ProtocolPlayerEasyTierID)
	}

	WriteProtocolResponse(conn, StatusOK, []byte(strings.Join(common, "\x00")))
}

func (s *ScaffoldingService) handleServerPort(conn net.Conn) {
	if s.mcPort == 0 {
		WriteProtocolResponse(conn, StatusServerNotStarted, nil)
		return
	}
	var buf [2]byte
	binary.BigEndian.PutUint16(buf[:], s.mcPort)
	WriteProtocolResponse(conn, StatusOK, buf[:])
}

func (s *ScaffoldingService) handlePlayerPing(conn net.Conn, body []byte) {
	var player PlayerInfo
	if err := json.Unmarshal(body, &player); err != nil {
		WriteProtocolResponse(conn, StatusBadRequest, nil)
		return
	}

	s.hostPlayerMu.Lock()
	if player.Kind == "" {
		player.Kind = KindGuest
	}
	isNew := false
	if _, exists := s.hostPlayers[player.MachineID]; !exists && player.Kind == KindGuest {
		isNew = true
	}
	s.hostPlayers[player.MachineID] = &playerEntry{
		info:     &player,
		lastSeen: time.Now(),
	}
	s.hostPlayerMu.Unlock()

	if isNew {
		s.eventEmitter.Emit("room.player_joined", player)
	}

	WriteProtocolResponse(conn, StatusOK, nil)
}

func (s *ScaffoldingService) handlePlayerProfilesList(conn net.Conn) {
	players := s.copyPlayers()

	data, err := json.Marshal(players)
	if err != nil {
		WriteProtocolResponse(conn, StatusUnknownError, []byte(err.Error()))
		return
	}
	WriteProtocolResponse(conn, StatusOK, data)
}
