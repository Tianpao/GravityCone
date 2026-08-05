package paperconnect

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/wlynxg/anet"
)

const rakNetDiscoveryPort = 19132
const rakNetPongPacketID byte = 0x1c

var rakNetMagic = [16]byte{0x00, 0xff, 0xff, 0x00, 0xfe, 0xfe, 0xfe, 0xfe, 0xfd, 0xfd, 0xfd, 0xfd, 0x12, 0x34, 0x56, 0x78}

type RakNetServerInfo struct {
	MOTD       string
	ServerName string
	LevelName  string
	GamePort   uint16
	ServerGUID int64
}

func ScanRakNetLAN(ctx context.Context, timeout time.Duration) (*RakNetServerInfo, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, fmt.Errorf("RakNet scan: listen udp: %w", err)
	}
	defer conn.Close()

	if rawConn, err := conn.SyscallConn(); err == nil {
		rawConn.Control(func(fd uintptr) {
			_ = SetBroadcast(fd)
		})
	}

	broadcastAddrs, _ := getBroadcastAddrs(rakNetDiscoveryPort)
	localAddrs := GetLocalAddrs(rakNetDiscoveryPort)
	pingPacket := buildUnconnectedPing()

	deadline := time.Now().Add(timeout)
	resultCh := make(chan *RakNetServerInfo, 1)
	errCh := make(chan error, 1)

	// On Windows, broadcasts don't loopback, so local unicast pings are essential.
	stopPing := make(chan struct{})
	defer close(stopPing)
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				for _, addr := range broadcastAddrs {
					conn.WriteToUDP(pingPacket, addr)
				}
				for _, addr := range localAddrs {
					conn.WriteToUDP(pingPacket, addr)
				}
			case <-stopPing:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		buf := make([]byte, 1500)
		for time.Now().Before(deadline) {
			if err := conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
				errCh <- err
				return
			}
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				errCh <- err
				return
			}

			if n < 1 || buf[0] != rakNetPongPacketID {
				continue
			}

			info, err := parseRakNetPong(buf[:n])
			if err != nil {
				continue
			}

			select {
			case resultCh <- info:
				return
			default:
			}
		}
		errCh <- fmt.Errorf("no RakNet server found on LAN after %v", timeout)
	}()

	select {
	case info := <-resultCh:
		parsed := parseRakNetMOTD(info.MOTD)
		if parsed != nil {
			parsed.ServerGUID = info.ServerGUID
		}
		return parsed, nil
	case err := <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func getBroadcastAddrs(port int) ([]*net.UDPAddr, error) {
	interfaces, err := anet.Interfaces()
	if err != nil {
		return nil, err
	}
	var addrs []*net.UDPAddr
	seen := make(map[string]bool)
	for _, iface := range interfaces {
		if !isPhysicalNIC(iface) {
			continue
		}
		ifaceAddrs, err := anet.InterfaceAddrsByInterface(&iface)
		if err != nil {
			continue
		}
		for _, a := range ifaceAddrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok || ipNet.IP.IsLoopback() || isEasyTierIP(ipNet.IP) {
				continue
			}
			ip4 := ipNet.IP.To4()
			if ip4 == nil || len(ipNet.Mask) != 4 {
				continue
			}
			broadcast := make(net.IP, 4)
			for i := range ip4 {
				broadcast[i] = ip4[i] | ^ipNet.Mask[i]
			}
			addrStr := fmt.Sprintf("%s:%d", broadcast.String(), port)
			if !seen[addrStr] {
				seen[addrStr] = true
				udpAddr, _ := net.ResolveUDPAddr("udp4", addrStr)
				if udpAddr != nil {
					addrs = append(addrs, udpAddr)
				}
			}
		}
	}
	return addrs, nil
}

func buildUnconnectedPing() []byte {
	buf := make([]byte, 33)
	buf[0] = 0x01
	binary.BigEndian.PutUint64(buf[1:], uint64(time.Now().UnixMilli()))
	copy(buf[9:], rakNetMagic[:])
	binary.BigEndian.PutUint64(buf[25:], uint64(rand.Int63()))
	return buf
}

func parseRakNetPong(data []byte) (*RakNetServerInfo, error) {
	if len(data) < 35 {
		return nil, fmt.Errorf("pong packet too short: %d bytes", len(data))
	}
	if data[0] != rakNetPongPacketID {
		return nil, fmt.Errorf("not a pong packet: id=%d", data[0])
	}
	serverGUID := int64(binary.BigEndian.Uint64(data[9:]))
	motdLen := int(binary.BigEndian.Uint16(data[33:]))
	if len(data) < 35+motdLen {
		return nil, fmt.Errorf("pong MOTD length mismatch")
	}
	motd := string(data[35 : 35+motdLen])
	return &RakNetServerInfo{
		MOTD:       motd,
		ServerGUID: serverGUID,
	}, nil
}

// Format: MCPE;ServerName;ProtocolVersion;VersionString;CurrentPlayers;MaxPlayers;ServerGUID;LevelName;GameMode;GameModeNum;PortIPv4;PortIPv6;
func parseRakNetMOTD(motd string) *RakNetServerInfo {
	parts := strings.Split(motd, ";")
	if len(parts) < 12 || parts[0] != "MCPE" {
		return nil
	}

	port, err := strconv.ParseUint(parts[10], 10, 16)
	if err != nil {
		return nil
	}

	serverGUID := int64(0)
	if guid, err := strconv.ParseInt(parts[6], 10, 64); err == nil {
		serverGUID = guid
	}

	return &RakNetServerInfo{
		MOTD:       motd,
		ServerName: parts[1],
		LevelName:  parts[7],
		GamePort:   uint16(port),
		ServerGUID: serverGUID,
	}
}

// isEasyTierIP 判断 IP 是否为 EasyTier 虚拟网络地址（10.144.144.x）。
func isEasyTierIP(ip net.IP) bool {
	ip4 := ip.To4()
	return ip4 != nil && ip4[0] == 10 && ip4[1] == 144 && ip4[2] == 144
}

// isPhysicalNIC reports whether the interface is a physical NIC (not a virtual /
// hypervisor / container adapter). Filters by known virtual MAC OUI prefixes and
// by interface name patterns.
func isPhysicalNIC(iface net.Interface) bool {
	if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagBroadcast == 0 {
		return false
	}
	for _, prefix := range []string{
		"veth", "docker", "br-", "tun", "tap", "wg", "vmnet", "vboxnet",
		"vEthernet", "Hyper-V", "VirtualBox", "VMware", "Loopback",
		"lo", "utun", "llw", "awdl", "anpi",
	} {
		if len(iface.Name) >= len(prefix) && strings.EqualFold(iface.Name[:len(prefix)], prefix) {
			return false
		}
	}
	if len(iface.HardwareAddr) >= 3 {
		oui := [3]byte{iface.HardwareAddr[0], iface.HardwareAddr[1], iface.HardwareAddr[2]}
		for _, prefix := range [][3]byte{
			{0x00, 0x05, 0x69}, // VMware
			{0x00, 0x0C, 0x29}, // VMware
			{0x00, 0x50, 0x56}, // VMware
			{0x00, 0x15, 0x5D}, // Hyper-V
			{0x08, 0x00, 0x27}, // VirtualBox
			{0x0A, 0x00, 0x27}, // VirtualBox
			{0x00, 0x1C, 0x42}, // Parallels
		} {
			if oui == prefix {
				return false
			}
		}
	}
	return true
}
