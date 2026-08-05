package paperconnect

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"

	mcstatus "github.com/andre-carbajal/go-mcstatus"

)

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

// broadcastRakNetFakeServer advertises the forwarded Bedrock server on the guest LAN.
// It answers discovery pings when port 19132 is available and always sends periodic
// unsolicited pongs from a separate broadcast socket.
//
// motdQueryAddr is where the real Bedrock server is queried for the actual MOTD:
// proxy 模式经本地转发(127.0.0.1:proxyPort)访问 host;direct 模式(TUN)直连
// host 虚拟 IP 的游戏端口。
func BroadcastRakNetFakeServer(ctx context.Context, stopCh <-chan struct{}, fallbackName string, proxyPort uint16, motdQueryAddr string, readyCh chan<- error) {
	serverGUID := rand.Int63()
	slog.Info("RakNet fake server starting", "guid", serverGUID, "proxyPort", proxyPort)

	bcConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		startupErr := fmt.Errorf("open RakNet broadcast socket: %w", err)
		slog.Error("RakNet fake server broadcast socket failed", "err", startupErr, "guid", serverGUID)
		readyCh <- startupErr
		return
	}
	defer bcConn.Close()
	if rawConn, err := bcConn.SyscallConn(); err == nil {
		_ = rawConn.Control(func(fd uintptr) {
			_ = SetBroadcast(fd)
		})
	}

	broadcastAddrs, _ := getBroadcastAddrs(rakNetDiscoveryPort)
	localAddrs := GetLocalAddrs(rakNetDiscoveryPort)
	slog.Info("RakNet fake server broadcast socket ready", "addr", bcConn.LocalAddr().String(),
		"broadcastAddrs", len(broadcastAddrs), "localAddrs", len(localAddrs), "guid", serverGUID)

	fallbackMOTD := buildFallbackBedrockMOTD(fallbackName, serverGUID, proxyPort)
	var motdMu sync.RWMutex
	motd := fallbackMOTD
	getMOTD := func() string {
		motdMu.RLock()
		defer motdMu.RUnlock()
		return motd
	}

	readyCh <- nil

	go func() {
		queriedMOTD, ok := queryBedrockMOTD(motdQueryAddr, fallbackName, serverGUID, proxyPort)
		if !ok {
			return
		}
		motdMu.Lock()
		motd = queriedMOTD
		motdMu.Unlock()
		slog.Info("RakNet fake server MOTD updated", "guid", serverGUID)
	}()

	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				_ = SetReuseAddr(fd)
				_ = SetBroadcast(fd)
			})
		},
	}
	listenConn, err := lc.ListenPacket(ctx, "udp4", fmt.Sprintf("0.0.0.0:%d", rakNetDiscoveryPort))
	if err != nil {
		slog.Warn("RakNet fake server ping responder unavailable; continuing active broadcast",
			"err", err, "port", rakNetDiscoveryPort, "guid", serverGUID)
	} else {
		defer listenConn.Close()
		slog.Info("RakNet fake server ping responder ready", "addr", listenConn.LocalAddr().String(), "guid", serverGUID)
		go func() {
			buf := make([]byte, 1500)
			for {
				n, addr, err := listenConn.ReadFrom(buf)
				if err != nil {
					return
				}
				if n < 25 || (buf[0] != 0x01 && buf[0] != 0x02) {
					continue
				}
				var magic [16]byte
				copy(magic[:], buf[9:25])
				if magic != rakNetMagic {
					continue
				}
				pong := buildUnconnectedPong(getMOTD(), serverGUID)
				binary.BigEndian.PutUint64(pong[1:], binary.BigEndian.Uint64(buf[1:9]))
				_, _ = listenConn.WriteTo(pong, addr)
			}
		}()
	}

	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			pongPacket := buildUnconnectedPong(getMOTD(), serverGUID)
			for _, addr := range broadcastAddrs {
				_, _ = bcConn.WriteToUDP(pongPacket, addr)
			}
			for _, addr := range localAddrs {
				_, _ = bcConn.WriteToUDP(pongPacket, addr)
			}
		}
	}
}

// queryBedrockMOTD queries a Bedrock server at the given address and returns
// a properly formatted MCPE MOTD string with the specified proxyPort.
func queryBedrockMOTD(address string, fallbackName string, serverGUID int64, proxyPort uint16) (string, bool) {
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
			proxyPort, proxyPort), true
	}

	slog.Warn("failed to query Bedrock server; retaining fallback MOTD", "address", address)
	return "", false
}

func buildFallbackBedrockMOTD(fallbackName string, serverGUID int64, proxyPort uint16) string {
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
