package paperconnect

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/df-mc/go-nethernet/discovery"
)

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
