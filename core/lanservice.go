package core

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/andre-carbajal/go-mcstatus"
	"golang.org/x/net/ipv4"
)

type LanServer struct {
	MOTD string `json:"motd"`
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

type LanService struct {
	mu       sync.Mutex
	servers  []LanServer
	conns    []*net.UDPConn
	pconns   []*ipv4.PacketConn
	stopCh   chan struct{}
	running  bool
	localIPs map[string]bool
}

func (s *LanService) StartDiscovery() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.servers = nil
	s.stopCh = make(chan struct{})
	s.mu.Unlock()

	group := net.IPv4(224, 0, 2, 60)

	ifaces, err := net.Interfaces()
	if err != nil {
		return fmt.Errorf("failed to list interfaces: %w", err)
	}

	var conns []*net.UDPConn
	var pconns []*ipv4.PacketConn
	localIPs := make(map[string]bool)
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.To4() == nil {
				continue
			}
			localIPs[ipNet.IP.To4().String()] = true
			bindAddr := &net.UDPAddr{IP: ipNet.IP.To4(), Port: 4445}
			conn, err := net.ListenPacket("udp4", bindAddr.String())
			if err != nil {
				continue
			}
			udpConn := conn.(*net.UDPConn)
			pc := ipv4.NewPacketConn(udpConn)
			if err := pc.JoinGroup(&iface, &net.UDPAddr{IP: group}); err != nil {
				pc.Close()
				continue
			}
			udpConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			conns = append(conns, udpConn)
			pconns = append(pconns, pc)
		}
	}

	if len(conns) == 0 {
		return fmt.Errorf("no suitable network interface found for multicast")
	}

	s.mu.Lock()
	s.conns = conns
	s.pconns = pconns
	s.localIPs = localIPs
	s.running = true
	s.mu.Unlock()

	go s.listen(conns, s.stopCh, localIPs)
	return nil
}

func (s *LanService) listen(conns []*net.UDPConn, stopCh chan struct{}, localIPs map[string]bool) {
	buf := make([]byte, 8192)
	for {
		for _, conn := range conns {
			conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			n, src, err := conn.ReadFromUDP(buf)
			if err != nil {
				continue
			}

			server := parseMCPacket(buf[:n], src)
			if !localIPs[server.IP] {
				continue
			}
			s.mu.Lock()
			found := false
			for i := range s.servers {
				if s.servers[i].IP == server.IP && s.servers[i].Port == server.Port {
					s.servers[i].MOTD = server.MOTD
					found = true
					break
				}
			}
			if !found {
				s.servers = append(s.servers, server)
			}
			s.mu.Unlock()
		}

		select {
		case <-stopCh:
			return
		default:
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
	for _, conn := range s.conns {
		conn.Close()
	}
	s.pconns = nil
	s.conns = nil
	s.localIPs = nil
	s.running = false
	s.servers = nil
}

func (s *LanService) GetDiscoveredServers() []LanServer {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]LanServer, len(s.servers))
	copy(result, s.servers)
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

func (s *LanService) CreateRoom(ip string, port int) error {
	if _, err := s.VerifyServer(ip, port); err != nil {
		return err
	}
	return fmt.Errorf("not implemented: starting easytier-core")
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
