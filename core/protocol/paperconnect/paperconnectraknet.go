package paperconnect

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"

	mcstatus "github.com/andre-carbajal/go-mcstatus"
	"github.com/df-mc/go-nethernet/discovery"

	"gravitycone/core/protocol/paperconnect/setsockopt"
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

func scanRakNetLAN(ctx context.Context, timeout time.Duration) (*RakNetServerInfo, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, fmt.Errorf("RakNet scan: listen udp: %w", err)
	}
	defer conn.Close()

	if rawConn, err := conn.SyscallConn(); err == nil {
		rawConn.Control(func(fd uintptr) {
			_ = setsockopt.SetBroadcast(fd)
		})
	}

	broadcastAddrs, _ := getBroadcastAddrs(rakNetDiscoveryPort)
	localAddrs := getLocalAddrs(rakNetDiscoveryPort)
	pingPacket := buildUnconnectedPing()

	deadline := time.Now().Add(timeout)
	resultCh := make(chan *RakNetServerInfo, 1)
	errCh := make(chan error, 1)

	// Background goroutine: periodically send broadcast + local unicast pings.
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

	// Main loop: collect pong responses.
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

// getBroadcastAddrs computes subnet broadcast addresses for all active interfaces
// plus the global broadcast address 255.255.255.255.
func getBroadcastAddrs(port int) ([]*net.UDPAddr, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var addrs []*net.UDPAddr
	seen := make(map[string]bool)
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagBroadcast == 0 {
			continue
		}
		ifaceAddrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range ifaceAddrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok || ipNet.IP.IsLoopback() {
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
	globalAddr, _ := net.ResolveUDPAddr("udp4", fmt.Sprintf("255.255.255.255:%d", port))
	if globalAddr != nil {
		addrs = append(addrs, globalAddr)
	}
	return addrs, nil
}

// getLocalAddrs returns all local IPv4 unicast addresses including 127.0.0.1.
// On Windows, broadcasts to 255.255.255.255 don't loopback, so local unicast pings
// are needed to discover servers on the same machine.
func getLocalAddrs(port int) []*net.UDPAddr {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var addrs []*net.UDPAddr
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		ifaceAddrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range ifaceAddrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipNet.IP.To4()
			if ip4 == nil {
				continue
			}
			udpAddr, _ := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", ip4.String(), port))
			if udpAddr != nil {
				addrs = append(addrs, udpAddr)
			}
		}
	}
	udpAddr, _ := net.ResolveUDPAddr("udp4", fmt.Sprintf("127.0.0.1:%d", port))
	if udpAddr != nil {
		addrs = append(addrs, udpAddr)
	}
	return addrs
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

// parseRakNetMOTD parses the Minecraft Bedrock MOTD string from a RakNet pong.
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

func buildUnconnectedPong(motd string, serverGUID int64) []byte {
	data := []byte(motd)
	buf := make([]byte, 35+len(data))
	buf[0] = rakNetPongPacketID
	binary.BigEndian.PutUint64(buf[1:], uint64(time.Now().UnixMilli()))
	binary.BigEndian.PutUint64(buf[9:], uint64(serverGUID))
	copy(buf[17:], rakNetMagic[:])
	binary.BigEndian.PutUint16(buf[33:], uint16(len(data)))
	copy(buf[35:], data)
	return buf
}

// broadcastRakNetFakeServer queries the host's Bedrock server through the EasyTier
// forwarded port to get real server info (MOTD, protocol, version, etc.), then runs a
// raw UDP handler on 127.0.0.1:19132 that responds ONLY to Unconnected Pings. Connection
// request packets (OPEN_CONNECTION_REQUEST_1/2) are silently ignored — a go-raknet
// listener would auto-complete the handshake but nobody calls Accept(), causing the
// client to time out.
//
// All pongs are sent from / received on 127.0.0.1:19132 only, so the client always
// resolves the server address as 127.0.0.1:{proxyPort}. The MOTD points to
// 127.0.0.1:proxyPort where EasyTier's port forward is listening.
func broadcastRakNetFakeServer(ctx context.Context, stopCh <-chan struct{}, fallbackName string, proxyPort uint16) {
	serverGUID := rand.Int63()
	slog.Info("RakNet fake server starting", "guid", serverGUID, "proxyPort", proxyPort)

	// 1. Query the host's Bedrock server through the forwarded port to get real info.
	motd := queryBedrockMOTD(fmt.Sprintf("127.0.0.1:%d", proxyPort), fallbackName, serverGUID, proxyPort)
	slog.Info("RakNet fake server MOTD", "motd", motd)

	// 2. Create a raw UDP socket on 127.0.0.1:19132 with SO_REUSEADDR so it can
	// coexist with the local MC client's own listener on the same port. Binding to
	// 127.0.0.1 (not 0.0.0.0) ensures all outgoing pongs carry 127.0.0.1 as source.
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				setsockopt.Setsockopt(fd) // SO_REUSEADDR
			})
		},
	}
	conn, err := lc.ListenPacket(ctx, "udp4", fmt.Sprintf("127.0.0.1:%d", rakNetDiscoveryPort))
	if err != nil {
		slog.Error("RakNet fake server listen failed", "err", err, "port", rakNetDiscoveryPort)
		return
	}
	defer conn.Close()
	slog.Info("RakNet fake server listening", "addr", conn.LocalAddr().String(), "guid", serverGUID)

	loopbackAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: rakNetDiscoveryPort}

	// 3. Goroutine: respond to Unconnected Pings from 127.0.0.1 ONLY. All other
	// packet types and source addresses are silently ignored.
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			// Only accept packets from 127.0.0.1.
			udpAddr, ok := addr.(*net.UDPAddr)
			if !ok || !udpAddr.IP.IsLoopback() {
				continue
			}
			if n < 25 {
				continue
			}
			if buf[0] != 0x01 && buf[0] != 0x02 {
				continue
			}
			var magic [16]byte
			copy(magic[:], buf[9:25])
			if magic != rakNetMagic {
				continue
			}
			pingTime := int64(binary.BigEndian.Uint64(buf[1:9]))
			pong := buildUnconnectedPong(motd, serverGUID)
			binary.BigEndian.PutUint64(pong[1:], uint64(pingTime))
			_, _ = conn.WriteTo(pong, addr)
		}
	}()

	// 4. Active broadcast: periodically send unsolicited pongs to 127.0.0.1:19132.
	// Because the socket is bound to 127.0.0.1, the source IP is always 127.0.0.1,
	// so the client resolves the server as 127.0.0.1:proxyPort (from MOTD) and
	// connects through the EasyTier forward.
	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			pongPacket := buildUnconnectedPong(motd, serverGUID)
			_, _ = conn.WriteTo(pongPacket, loopbackAddr)
		}
	}
}

// queryBedrockMOTD queries a Bedrock server at the given address and returns
// a properly formatted MCPE MOTD string with the specified proxyPort.
// Falls back to hardcoded defaults if the query fails.
func queryBedrockMOTD(address string, fallbackName string, serverGUID int64, proxyPort uint16) string {
	for attempt := 0; attempt < 15; attempt++ {
		bs, err := mcstatus.NewBedrockServer(address)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		raw, err := bs.Status()
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		resp, ok := raw.(*mcstatus.BedrockStatusResponse)
		if !ok {
			time.Sleep(2 * time.Second)
			continue
		}

		motdLine1 := resp.MOTD
		motdLine2 := resp.MapName
		if i := strings.Index(resp.MOTD, "\n"); i >= 0 {
			motdLine1 = resp.MOTD[:i]
			motdLine2 = strings.TrimSpace(resp.MOTD[i+1:])
		}
		if motdLine1 == "" {
			motdLine1 = fallbackName
		}
		if motdLine2 == "" {
			motdLine2 = resp.MapName
		}

		gmNum := gamemodeToNum(resp.Gamemode)

		slog.Info("queried Bedrock server", "address", address,
			"motd", resp.MOTD, "protocol", resp.Protocol, "version", resp.Version,
			"online", resp.Online, "max", resp.Max, "mapName", resp.MapName, "gamemode", resp.Gamemode)

		return fmt.Sprintf("MCPE;%s;%d;%s;%d;%d;%d;%s;%s;%d;%d;%d;",
			motdLine1, resp.Protocol, resp.Version,
			resp.Online, resp.Max, serverGUID,
			motdLine2, resp.Gamemode, gmNum,
			proxyPort, proxyPort)
	}

	// Fallback: hardcoded defaults.
	slog.Warn("failed to query Bedrock server, using fallback MOTD", "address", address)
	return fmt.Sprintf("MCPE;%s;589;1.20.0;1;20;%d;%s;Survival;0;%d;%d;",
		fallbackName, serverGUID, fallbackName, proxyPort, proxyPort)
}

func gamemodeToNum(gm string) int {
	switch strings.ToLower(gm) {
	case "survival":
		return 0
	case "creative":
		return 1
	case "adventure":
		return 2
	case "survivalviewer":
		return 3
	case "creativeviewer":
		return 4
	default:
		return 5
	}
}

var errNoNetherNet = fmt.Errorf("no NetherNet server found")

// detectNetherNet discovers a local Minecraft Bedrock NetherNet server and
// returns its network ID. The returned listener must be closed by the caller.
func detectNetherNetWithID(ctx context.Context) (uint64, error) {
	cfg := discovery.ListenConfig{
		NetworkID: uint64(time.Now().UnixNano()),
		BroadcastAddress: &net.UDPAddr{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: 7551,
		},
	}
	l, err := cfg.Listen(":0")
	if err != nil {
		return 0, err
	}
	defer l.Close()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for id := range l.Responses() {
			return id, nil
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return 0, errNoNetherNet
}

func detectNetherNet(ctx context.Context) bool {
	_, err := detectNetherNetWithID(ctx)
	return err == nil
}
