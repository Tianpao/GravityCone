package paperconnect

import (
	"context"
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
					log.Error("nethernet read error", "err", err, "packets_forwarded", nnPkCount.Load())
				}
				return
			}
			n := nnPkCount.Add(1)
			log.Info("nn→rk", "packet_size", len(pk), "total", n, "first_bytes", fmt.Sprintf("%x", truncateBytes(pk, 20)))
			if err := writeTunnelPacket(rkConn, pk); err != nil {
				if !isClosedErr(err) && ctx.Err() == nil {
					log.Error("raknet write error", "err", err, "packets_forwarded", nnPkCount.Load())
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
					log.Error("raknet read error", "err", err, "packets_forwarded", rkPkCount.Load())
				}
				return
			}
			n := rkPkCount.Add(1)
			log.Info("rk→nn", "packet_size", len(pk), "total", n, "first_bytes", fmt.Sprintf("%x", truncateBytes(pk, 20)))
			if _, err := nnConn.Write(pk); err != nil {
				if !isClosedErr(err) && ctx.Err() == nil {
					log.Error("nethernet write error", "err", err, "packets_forwarded", rkPkCount.Load())
				}
				return
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				log.Info("proxy status", "nn→rk", nnPkCount.Load(), "rk→nn", rkPkCount.Load())
			}
		}
	}()

	<-ctx.Done()
	log.Info("proxy session closed")
}

func dialLocalNetherNet(ctx context.Context) (*nethernet.Conn, error) {
	cfg := discovery.ListenConfig{
		NetworkID: randomID(),
	}
	l, err := cfg.Listen(":0")
	if err != nil {
		return nil, fmt.Errorf("start discovery: %w", err)
	}
	defer l.Close()

	slog.Info("discovering NetherNet servers on LAN (broadcast to 255.255.255.255:7551)...")

	var targetID uint64
	found := false
	for i := 0; i < 60; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		for id, data := range l.Responses() {
			var sd discovery.ServerData
			if err := sd.UnmarshalBinary(data); err != nil {
				slog.Info("found server (unparseable data)", "network_id", id)
			} else {
				slog.Info("found NetherNet server", "network_id", id, "name", sd.ServerName, "level", sd.LevelName)
			}
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

func truncateBytes(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}
