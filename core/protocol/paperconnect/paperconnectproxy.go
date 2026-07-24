package paperconnect

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/df-mc/go-nethernet"
	"github.com/df-mc/go-nethernet/discovery"
	raknet "github.com/sandertv/go-raknet"
)

const maxHostSessions = 20

// proxyTCPPackets proxies packets between a local NetherNet connection and a
// TCP tunnel to the remote peer. Each packet is prefixed with a 4-byte
// big-endian length header.
func proxyTCPPackets(parentCtx context.Context, log *slog.Logger, nnConn *nethernet.Conn, tcpConn net.Conn) {
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = nnConn.Close()
		_ = tcpConn.Close()
	}()

	var nnPkCount, tcpPkCount atomic.Int64

	// nn → tcp
	go func() {
		defer cancel()
		for {
			pk, err := nnConn.ReadPacket()
			if err != nil {
				if !isClosedErr(err) && ctx.Err() == nil {
					log.Error("nethernet read error", "err", err, "nn_packets", nnPkCount.Load())
				}
				return
			}
			nnPkCount.Add(1)
			if err := writeTCPFrame(tcpConn, pk); err != nil {
				if !isClosedErr(err) && ctx.Err() == nil {
					log.Error("tcp write error", "err", err, "nn_packets", nnPkCount.Load())
				}
				return
			}
		}
	}()

	// tcp → nn
	go func() {
		defer cancel()
		for {
			pk, err := readTCPFrame(tcpConn)
			if err != nil {
				if !isClosedErr(err) && ctx.Err() == nil {
					log.Error("tcp read error", "err", err, "tcp_packets", tcpPkCount.Load())
				}
				return
			}
			tcpPkCount.Add(1)
			if _, err := nnConn.Write(pk); err != nil {
				if !isClosedErr(err) && ctx.Err() == nil {
					log.Error("nethernet write error", "err", err, "tcp_packets", tcpPkCount.Load())
				}
				return
			}
		}
	}()

	<-ctx.Done()
}

// proxyPackets forwards packets between a NetherNet connection and a RakNet
// connection, preserving packet boundaries. The RakNet side uses the tunnel
// protocol (packet type 1 for small packets, chunked type 2 for large ones)
// to handle NetherNet packets that may exceed RakNet's MTU.
func proxyPackets(parentCtx context.Context, log *slog.Logger, nnConn *nethernet.Conn, rkConn *raknet.Conn) {
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = nnConn.Close()
		_ = rkConn.Close()
	}()

	var nnPkCount, rkPkCount atomic.Int64

	go func() {
		defer cancel()
		for {
			pk, err := nnConn.ReadPacket()
			if err != nil {
				if !isClosedErr(err) && ctx.Err() == nil {
					log.Error("nethernet read error", "err", err, "nn_packets", nnPkCount.Load())
				}
				return
			}
			nnPkCount.Add(1)
			if err := writeTunnelPacket(rkConn, pk); err != nil {
				if !isClosedErr(err) && ctx.Err() == nil {
					log.Error("raknet write error", "err", err, "nn_packets", nnPkCount.Load())
				}
				return
			}
		}
	}()

	go func() {
		defer cancel()
		reader := newTunnelReader(rkConn)
		for {
			pk, err := reader.ReadPacket()
			if err != nil {
				if !isClosedErr(err) && ctx.Err() == nil {
					log.Error("raknet read error", "err", err, "rk_packets", rkPkCount.Load())
				}
				return
			}
			rkPkCount.Add(1)
			if _, err := nnConn.Write(pk); err != nil {
				if !isClosedErr(err) && ctx.Err() == nil {
					log.Error("nethernet write error", "err", err, "rk_packets", rkPkCount.Load())
				}
				return
			}
		}
	}()

	<-ctx.Done()
}

func writeTCPFrame(conn net.Conn, data []byte) error {
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(data)))
	if _, err := conn.Write(header); err != nil {
		return err
	}
	_, err := conn.Write(data)
	return err
}

func readTCPFrame(conn net.Conn) ([]byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header)
	if length > 16*1024*1024 {
		return nil, fmt.Errorf("frame too large: %d", length)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, err
	}
	return data, nil
}

// dialLocalNetherNet discovers a local Minecraft Bedrock NetherNet server and
// dials it. Broadcasts to 127.0.0.1:7551 for reliable local discovery.
func dialLocalNetherNet(ctx context.Context) (*nethernet.Conn, error) {
	cfg := discovery.ListenConfig{
		NetworkID: randomID(),
		BroadcastAddress: &net.UDPAddr{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: 7551,
		},
	}
	l, err := cfg.Listen(":0")
	if err != nil {
		return nil, fmt.Errorf("start discovery: %w", err)
	}
	defer l.Close()

	var targetID uint64
	found := false
	for i := 0; i < 60; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		for id := range l.Responses() {
			targetID = id
			found = true
			break
		}
		if found {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !found {
		return nil, fmt.Errorf("no NetherNet server found on 127.0.0.1:7551 after 30 seconds")
	}

	dialer := nethernet.Dialer{
		DisableTrickleICE:       true,
		AllowIdentitylessServer: true,
	}
	dialCtx, dialCancel := context.WithTimeout(ctx, 30*time.Second)
	defer dialCancel()

	conn, err := dialer.DialContext(dialCtx, strconv.FormatUint(targetID, 10), l)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	return conn, nil
}

func randomID() uint64 {
	return uint64(time.Now().UnixNano())
}

func isClosedErr(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF)
}
