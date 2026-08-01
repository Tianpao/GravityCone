package paperconnect

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	raknet "github.com/sandertv/go-raknet"
)

// TestRelayRakNetPackets verifies that relayRakNetPackets forwards packets
// bidirectionally between two RakNet connections (the direct-mode guest
// relay: local Minecraft client <-> host through the tunnel).
func TestRelayRakNetPackets(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Two independent RakNet connections: A (relay's local side) and
	// B (relay's remote side). Each has its own client.
	lnA, err := (raknet.ListenConfig{MaxMTU: rakNetMTU, ErrorLog: slog.Default()}).Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lnA.Close()
	lnB, err := (raknet.ListenConfig{MaxMTU: rakNetMTU, ErrorLog: slog.Default()}).Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lnB.Close()

	acceptA := make(chan *raknet.Conn, 1)
	acceptB := make(chan *raknet.Conn, 1)
	go func() {
		if c, err := lnA.Accept(); err == nil {
			acceptA <- c.(*raknet.Conn)
		}
	}()
	go func() {
		if c, err := lnB.Accept(); err == nil {
			acceptB <- c.(*raknet.Conn)
		}
	}()

	clientA, err := (raknet.Dialer{MaxMTU: rakNetMTU, ErrorLog: slog.Default()}).Dial(lnA.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer clientA.Close()
	clientB, err := (raknet.Dialer{MaxMTU: rakNetMTU, ErrorLog: slog.Default()}).Dial(lnB.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer clientB.Close()

	var connA, connB *raknet.Conn
	select {
	case connA = <-acceptA:
	case <-time.After(5 * time.Second):
		t.Fatal("listener A did not accept client")
	}
	select {
	case connB = <-acceptB:
	case <-time.After(5 * time.Second):
		t.Fatal("listener B did not accept client")
	}

	go relayRakNetPackets(ctx, slog.Default(), connA, connB)

	// Local -> remote (clientA -> clientB).
	payloadA := bytes.Repeat([]byte{0x11}, 2048)
	gotB := make(chan []byte, 1)
	go func() {
		if pk, err := clientB.ReadPacket(); err == nil {
			gotB <- pk
		}
	}()
	if _, err := clientA.Write(payloadA); err != nil {
		t.Fatal(err)
	}
	select {
	case pk := <-gotB:
		if !bytes.Equal(pk, payloadA) {
			t.Fatal("relayed packet A->B differs")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("clientB did not receive relayed packet")
	}

	// Remote -> local (clientB -> clientA).
	payloadB := bytes.Repeat([]byte{0x22}, 4096)
	gotA := make(chan []byte, 1)
	go func() {
		if pk, err := clientA.ReadPacket(); err == nil {
			gotA <- pk
		}
	}()
	if _, err := clientB.Write(payloadB); err != nil {
		t.Fatal(err)
	}
	select {
	case pk := <-gotA:
		if !bytes.Equal(pk, payloadB) {
			t.Fatal("relayed packet B->A differs")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("clientA did not receive relayed packet")
	}

	// Closing the relay context tears both connections down.
	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := connA.ReadPacket(); err != nil {
			return // relay closed connA as expected
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("relay did not close connections after context cancellation")
}
