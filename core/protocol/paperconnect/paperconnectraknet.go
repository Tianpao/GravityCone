package paperconnect

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"

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
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var opErr error
			err := c.Control(func(fd uintptr) {
				opErr = setsockopt.Setsockopt(fd)
			})
			if err != nil {
				return err
			}
			return opErr
		},
	}
	conn, err := lc.ListenPacket(ctx, "udp", fmt.Sprintf(":%d", rakNetDiscoveryPort))
	if err != nil {
		conn, err = net.ListenPacket("udp", ":0")
		if err != nil {
			return nil, fmt.Errorf("RakNet scan: bind failed: %w", err)
		}
	}

	deadline := time.Now().Add(timeout)
	resultCh := make(chan *RakNetServerInfo, 1)
	errCh := make(chan error, 1)

	go func() {
		buf := make([]byte, 2048)
		for {
			if time.Now().After(deadline) {
				errCh <- fmt.Errorf("no RakNet server found on LAN after %v", timeout)
				return
			}
			select {
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			default:
			}

			conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			n, addr, err := conn.ReadFrom(buf)
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
			_ = addr

			select {
			case resultCh <- info:
				return
			default:
			}
		}
	}()

	// Also send active pings to provoke responses.
	go func() {
		pingBuf := buildUnconnectedPing()
		for {
			if time.Now().After(deadline) {
				return
			}
			select {
			case <-ctx.Done():
				return
			default:
			}
			broadcastAddr := &net.UDPAddr{IP: net.IPv4bcast, Port: rakNetDiscoveryPort}
			conn.WriteTo(pingBuf, broadcastAddr)
			time.Sleep(500 * time.Millisecond)
		}
	}()

	select {
	case info := <-resultCh:
		conn.Close()
		parsed := parseRakNetMOTD(info.MOTD)
		if parsed != nil {
			parsed.ServerGUID = info.ServerGUID
		}
		return parsed, nil
	case err := <-errCh:
		conn.Close()
		return nil, err
	case <-ctx.Done():
		conn.Close()
		return nil, ctx.Err()
	}
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

// broadcastRakNetFakeServer periodically sends unconnected pong advertisements
// to the local Minecraft Bedrock client on 127.0.0.1:19132.
// The MOTD in the pong points to 127.0.0.1:proxyPort so the client connects through the forwarded port.
func broadcastRakNetFakeServer(ctx context.Context, stopCh <-chan struct{}, serverName string, proxyPort uint16) {
	conn, err := net.ListenPacket("udp", ":0")
	if err != nil {
		return
	}

	serverGUID := rand.Int63()
	targetAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: rakNetDiscoveryPort}

	// Build MOTD pointing to the local forwarded port.
	// Format: MCPE;ServerName;ProtocolVersion;VersionString;CurrentPlayers;MaxPlayers;ServerGUID;LevelName;GameMode;GameModeNum;PortIPv4;PortIPv6;
	motd := fmt.Sprintf("MCPE;%s;589;1.20.0;1;20;%d;%s;Survival;0;%d;%d;",
		serverName, serverGUID, serverName, proxyPort, proxyPort)

	pongBuf := buildUnconnectedPong(motd, serverGUID)

	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = conn.WriteTo(pongBuf, targetAddr)
		}
	}
}

func detectNetherNet(ctx context.Context) bool {
	cfg := discovery.ListenConfig{
		NetworkID: uint64(time.Now().UnixNano()),
	}
	l, err := cfg.Listen(":0")
	if err != nil {
		return false
	}
	defer l.Close()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(l.Responses()) > 0 {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(500 * time.Millisecond):
		}
	}
	return false
}
