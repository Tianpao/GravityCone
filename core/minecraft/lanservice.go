package minecraft

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/andre-carbajal/go-mcstatus"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"

	"gravitycone/core/utils"
)

type LanServer struct {
	MOTD string `json:"motd"`
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

type lanServerEntry struct {
	server   LanServer
	lastSeen time.Time
}

func NewLanService(emitter utils.EventEmitter) *LanService {
	if emitter == nil {
		emitter = utils.NilEventEmitter{}
	}
	return &LanService{eventEmitter: emitter}
}

type LanService struct {
	eventEmitter utils.EventEmitter
	mu           sync.Mutex
	entries      []lanServerEntry
	conns        []*net.UDPConn
	pconns       []*ipv4.PacketConn
	pconnsV6     []*ipv6.PacketConn
	stopCh       chan struct{}
	running      bool
	localIPs     map[string]bool
}

func (s *LanService) SetEventEmitter(emitter utils.EventEmitter) {
	if emitter == nil {
		emitter = utils.NilEventEmitter{}
	}
	s.eventEmitter = emitter
}

const lanServerTimeout = 30 * time.Second

func (s *LanService) StartDiscovery() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.entries = nil
	s.stopCh = make(chan struct{})
	s.mu.Unlock()

	group := net.IPv4(224, 0, 2, 60)
	groupV6 := net.ParseIP("ff75:230::60")

	ifaces, err := net.Interfaces()
	if err != nil {
		return fmt.Errorf("failed to list interfaces: %w", err)
	}

	localIPs := map[string]bool{"127.0.0.1": true}
	var bindAddresses []string
	for i := range ifaces {
		iface := &ifaces[i]
		addrs, err := iface.Addrs()
		if err != nil {
			slog.Warn("failed to inspect LAN discovery interface", "interface", iface.Name, "error", err)
			continue
		}

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.IsUnspecified() {
				continue
			}
			ip := ipNet.IP
			localIPs[ip.String()] = true
			if ip.To4() != nil && iface.Flags&net.FlagUp != 0 && iface.Flags&net.FlagLoopback == 0 && iface.Flags&net.FlagMulticast != 0 {
				bindAddresses = append(bindAddresses, ip.String())
			}
		}
	}
	bindAddresses = append(bindAddresses, "0.0.0.0")

	listenConfig := net.ListenConfig{
		Control: func(_ string, _ string, rawConn syscall.RawConn) error {
			var controlErr error
			if err := rawConn.Control(func(fd uintptr) {
				controlErr = setReuseAddr(fd)
			}); err != nil {
				return err
			}
			return controlErr
		},
	}
	var conns []*net.UDPConn
	var pconns []*ipv4.PacketConn
	var pconnsV6 []*ipv6.PacketConn
	for _, address := range bindAddresses {
		conn, err := listenConfig.ListenPacket(context.Background(), "udp4", net.JoinHostPort(address, "4445"))
		if err != nil {
			slog.Warn("failed to bind LAN discovery socket", "address", address, "error", err)
			continue
		}
		udpConn := conn.(*net.UDPConn)
		pc := ipv4.NewPacketConn(udpConn)
		if err := pc.JoinGroup(nil, &net.UDPAddr{IP: group}); err != nil {
			pc.Close()
			slog.Warn("failed to join LAN multicast group", "address", address, "group", group, "error", err)
			continue
		}
		slog.Info("joined LAN multicast group", "address", address, "group", group)
		conns = append(conns, udpConn)
		pconns = append(pconns, pc)
	}
	if conn, err := listenConfig.ListenPacket(context.Background(), "udp6", "[::]:4445"); err != nil {
		slog.Warn("failed to bind IPv6 LAN discovery socket", "error", err)
	} else {
		udpConn := conn.(*net.UDPConn)
		pc := ipv6.NewPacketConn(udpConn)
		if err := pc.JoinGroup(nil, &net.UDPAddr{IP: groupV6}); err != nil {
			pc.Close()
			slog.Warn("failed to join IPv6 LAN multicast group", "group", groupV6, "error", err)
		} else {
			slog.Info("joined IPv6 LAN multicast group", "address", udpConn.LocalAddr(), "group", groupV6)
			conns = append(conns, udpConn)
			pconnsV6 = append(pconnsV6, pc)
		}
	}
	if len(conns) == 0 {
		return fmt.Errorf("failed to join LAN discovery multicast group")
	}

	s.mu.Lock()
	s.conns = conns
	s.pconns = pconns
	s.pconnsV6 = pconnsV6
	s.localIPs = localIPs
	s.running = true
	s.mu.Unlock()

	for _, conn := range conns {
		go s.listen(conn, s.stopCh, localIPs)
	}
	go s.cleanupLoop(s.stopCh)
	return nil
}

func (s *LanService) listen(conn *net.UDPConn, stopCh chan struct{}, localIPs map[string]bool) {
	buf := make([]byte, 8192)
	for {
		conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-stopCh:
				return
			default:
			}
			if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
				slog.Warn("LAN discovery socket read failed", "error", err)
			}
			continue
		}

		server := parseMCPacket(buf[:n], src)
		isLocal := localIPs[server.IP]
		slog.Info("received LAN discovery packet", "source", src.String(), "local", isLocal, "motd", server.MOTD, "port", server.Port, "payload", logPayload(buf[:n]))
		if !isLocal {
			slog.Info("discarded non-local LAN discovery packet", "source", src.String())
			continue
		}
		s.mu.Lock()
		found := false
		for i := range s.entries {
			if s.entries[i].server.IP == server.IP && s.entries[i].server.Port == server.Port {
				s.entries[i].server.MOTD = server.MOTD
				s.entries[i].lastSeen = time.Now()
				found = true
				break
			}
		}
		if !found {
			s.entries = append(s.entries, lanServerEntry{server: server, lastSeen: time.Now()})
			s.eventEmitter.Emit("lan.server_found", server)
		}
		s.mu.Unlock()
	}
}

func (s *LanService) cleanupLoop(stopCh chan struct{}) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			now := time.Now()
			var kept []lanServerEntry
			for _, e := range s.entries {
				if now.Sub(e.lastSeen) > lanServerTimeout {
					s.eventEmitter.Emit("lan.server_lost", map[string]interface{}{"ip": e.server.IP, "port": e.server.Port})
				} else {
					kept = append(kept, e)
				}
			}
			s.entries = kept
			s.mu.Unlock()
		case <-stopCh:
			return
		}
	}
}

func (s *LanService) StopDiscovery() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	close(s.stopCh)
	for _, pc := range s.pconns {
		pc.Close()
	}
	for _, pc := range s.pconnsV6 {
		pc.Close()
	}
	for _, conn := range s.conns {
		conn.Close()
	}
	s.pconns = nil
	s.pconnsV6 = nil
	s.conns = nil
	s.localIPs = nil
	s.running = false
	s.entries = nil
}

func (s *LanService) GetDiscoveredServers() []LanServer {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]LanServer, len(s.entries))
	for i, e := range s.entries {
		result[i] = e.server
	}
	return result
}

func (s *LanService) VerifyServer(ip string, port int) (string, error) {
	server, err := mcstatus.NewJavaServer(fmt.Sprintf("%s:%d", ip, port))
	if err != nil {
		return "", fmt.Errorf("无法连接到服务器")
	}

	status, err := server.Status()
	if err != nil {
		return "", fmt.Errorf("此端口非 Minecraft 房间")
	}

	resp, ok := status.(*mcstatus.JavaStatusResponse)
	if !ok {
		return "", fmt.Errorf("此端口非 Minecraft 房间")
	}

	return resp.Version.Name, nil
}

func logPayload(data []byte) string {
	const maxLength = 256
	if len(data) > maxLength {
		return string(data[:maxLength]) + "..."
	}
	return string(data)
}

func parseMCPacket(data []byte, src *net.UDPAddr) LanServer {
	msg := strings.TrimRight(string(data), "\x00\n\r")
	motd := ""
	port := 25565

	if mStart := strings.Index(msg, "[MOTD]"); mStart != -1 {
		rest := msg[mStart+6:]
		if mEnd := strings.Index(rest, "[/MOTD]"); mEnd != -1 {
			motd = rest[:mEnd]
		}
	}

	if aStart := strings.Index(msg, "[AD]"); aStart != -1 {
		rest := msg[aStart+4:]
		if aEnd := strings.Index(rest, "[/AD]"); aEnd != -1 {
			if p, err := strconv.Atoi(rest[:aEnd]); err == nil {
				port = p
			}
		}
	}

	return LanServer{MOTD: motd, IP: src.IP.String(), Port: port}
}
