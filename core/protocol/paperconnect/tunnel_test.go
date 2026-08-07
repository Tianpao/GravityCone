package paperconnect

import (
	"bytes"
	"fmt"
	"log/slog"
	"testing"
	"time"

	raknet "github.com/sandertv/go-raknet"
)

func TestTunnelForwardsLargePacket(t *testing.T) {
	listener, err := (raknet.ListenConfig{
		MaxMTU:   rakNetMTU,
		ErrorLog: slog.Default(),
	}).Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	accepted := make(chan *raknet.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn.(*raknet.Conn)
		}
	}()

	client, err := (raknet.Dialer{
		MaxMTU:   rakNetMTU,
		ErrorLog: slog.Default(),
	}).Dial(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var server *raknet.Conn
	select {
	case server = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("listener did not accept client")
	}
	defer server.Close()

	payload := bytes.Repeat([]byte{0xA5}, 26_185)
	result := make(chan struct {
		packet []byte
		err    error
	}, 1)
	go func() {
		packet, err := newTunnelReader(server).ReadPacket()
		result <- struct {
			packet []byte
			err    error
		}{packet, err}
	}()
	if err := newTunnelWriter(client).Write(payload); err != nil {
		t.Fatal(err)
	}

	select {
	case received := <-result:
		if received.err != nil {
			t.Fatal(received.err)
		}
		if !bytes.Equal(received.packet, payload) {
			t.Fatal("received payload differs")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not receive tunnelled large packet")
	}
}

// TestTunnelForwardsBulkPacketUnordered verifies that a packet larger than
// tunnelBulkThreshold round-trips intact. Such packets take the reliable-
// unordered path (WriteUnordered) and are reassembled by the reader from their
// (seq, index) parts.
func TestTunnelForwardsBulkPacketUnordered(t *testing.T) {
	listener, err := (raknet.ListenConfig{
		MaxMTU:   rakNetMTU,
		ErrorLog: slog.Default(),
	}).Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	accepted := make(chan *raknet.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn.(*raknet.Conn)
		}
	}()

	client, err := (raknet.Dialer{
		MaxMTU:   rakNetMTU,
		ErrorLog: slog.Default(),
	}).Dial(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var server *raknet.Conn
	select {
	case server = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("listener did not accept client")
	}
	defer server.Close()

	// Exceeds tunnelBulkThreshold (64KB) so chunks are sent unordered.
	payload := bytes.Repeat([]byte{0x5A}, tunnelBulkThreshold+tunnelChunkSize+123)
	result := make(chan struct {
		packet []byte
		err    error
	}, 1)
	go func() {
		packet, err := newTunnelReader(server).ReadPacket()
		result <- struct {
			packet []byte
			err    error
		}{packet, err}
	}()
	if err := newTunnelWriter(client).Write(payload); err != nil {
		t.Fatal(err)
	}

	select {
	case received := <-result:
		if received.err != nil {
			t.Fatal(received.err)
		}
		if !bytes.Equal(received.packet, payload) {
			t.Fatal("received bulk payload differs")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not receive tunnelled bulk packet")
	}
}

// TestTunnelPreservesMessageOrder verifies that a mix of small and bulk
// packets is delivered in send order, even though the bulk packets take the
// unordered path and may complete out of order on arrival.
func TestTunnelPreservesMessageOrder(t *testing.T) {
	listener, err := (raknet.ListenConfig{
		MaxMTU:   rakNetMTU,
		ErrorLog: slog.Default(),
	}).Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	accepted := make(chan *raknet.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn.(*raknet.Conn)
		}
	}()

	client, err := (raknet.Dialer{
		MaxMTU:   rakNetMTU,
		ErrorLog: slog.Default(),
	}).Dial(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var server *raknet.Conn
	select {
	case server = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("listener did not accept client")
	}
	defer server.Close()

	const n = 6
	payloads := make([][]byte, n)
	for i := range payloads {
		if i%2 == 1 {
			// Bulk packets (unordered path) with a per-message marker prefix.
			payloads[i] = append(bytes.Repeat([]byte{byte(i)}, tunnelBulkThreshold), byte(i))
		} else {
			payloads[i] = []byte{byte(i), byte(i + 1)}
		}
	}

	reader := newTunnelReader(server)
	writer := newTunnelWriter(client)

	// Send all packets, then read them all back and check order.
	sendDone := make(chan error, 1)
	go func() {
		for _, p := range payloads {
			if err := writer.Write(p); err != nil {
				sendDone <- err
				return
			}
		}
		sendDone <- nil
	}()
	if err := <-sendDone; err != nil {
		t.Fatal(err)
	}

	for i := 0; i < n; i++ {
		got, err := reader.ReadPacket()
		if err != nil {
			t.Fatalf("read packet %d: %v", i, err)
		}
		if !bytes.Equal(got, payloads[i]) {
			t.Fatalf("packet %d out of order or corrupted: got %d bytes, want %d bytes", i, len(got), len(payloads[i]))
		}
	}
}

func BenchmarkTunnelLargePacket(b *testing.B) {
	listener, err := (raknet.ListenConfig{
		MaxMTU:   rakNetMTU,
		ErrorLog: slog.Default(),
	}).Listen("127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	defer listener.Close()

	accepted := make(chan *raknet.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn.(*raknet.Conn)
		}
	}()

	client, err := (raknet.Dialer{
		MaxMTU:   rakNetMTU,
		ErrorLog: slog.Default(),
	}).Dial(listener.Addr().String())
	if err != nil {
		b.Fatal(err)
	}
	defer client.Close()

	var server *raknet.Conn
	select {
	case server = <-accepted:
	case <-time.After(5 * time.Second):
		b.Fatal("listener did not accept client")
	}
	defer server.Close()

	payload := bytes.Repeat([]byte{0xA5}, 26_185)

	// Pre-warm: let one packet through so MTU negotiation completes.
	if err := newTunnelWriter(client).Write(payload); err != nil {
		b.Fatal(err)
	}
	reader := newTunnelReader(server)
	if _, err := reader.ReadPacket(); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		if err := newTunnelWriter(client).Write(payload); err != nil {
			b.Fatal(err)
		}
		// Read back to apply backpressure and avoid RakNet flow-control overflow.
		// Small delay to prevent RakNet ACK queue overflow under
		// benchmark's sustained maximum throughput.
		time.Sleep(time.Millisecond)
		if _, err := reader.ReadPacket(); err != nil {
			b.Fatal(err)
		}
	}
}

func TestTunnelSessionsRemainIsolated(t *testing.T) {
	listener, err := (raknet.ListenConfig{
		MaxMTU:   rakNetMTU,
		ErrorLog: slog.Default(),
	}).Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	accepted := make(chan *raknet.Conn, 2)
	for range 2 {
		go func() {
			conn, err := listener.Accept()
			if err == nil {
				accepted <- conn.(*raknet.Conn)
			}
		}()
	}

	clients := make([]*raknet.Conn, 2)
	for i := range clients {
		clients[i], err = (raknet.Dialer{
			MaxMTU:   rakNetMTU,
			ErrorLog: slog.Default(),
		}).Dial(listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer clients[i].Close()
	}

	servers := make([]*raknet.Conn, 2)
	for i := range servers {
		select {
		case servers[i] = <-accepted:
			defer servers[i].Close()
		case <-time.After(5 * time.Second):
			t.Fatal("listener did not accept clients")
		}
	}

	identities := [][]byte{{1}, {2}}
	for i := range clients {
		if err := newTunnelWriter(clients[i]).Write(identities[i]); err != nil {
			t.Fatal(err)
		}
	}

	clientResults := make(chan struct {
		index  int
		packet []byte
		err    error
	}, len(clients))
	for i := range clients {
		go func(i int) {
			packet, err := newTunnelReader(clients[i]).ReadPacket()
			clientResults <- struct {
				index  int
				packet []byte
				err    error
			}{i, packet, err}
		}(i)
	}

	serverErrs := make(chan error, len(servers))
	for i := range servers {
		go func(conn *raknet.Conn) {
			identity, err := newTunnelReader(conn).ReadPacket()
			if err != nil {
				serverErrs <- err
				return
			}
			if len(identity) != 1 || (identity[0] != 1 && identity[0] != 2) {
				serverErrs <- fmt.Errorf("unexpected tenant identity %x", identity)
				return
			}
			payload := bytes.Repeat([]byte{identity[0]}, 26_185)
			serverErrs <- newTunnelWriter(conn).Write(payload)
		}(servers[i])
	}

	for range servers {
		if err := <-serverErrs; err != nil {
			t.Fatal(err)
		}
	}
	for range clients {
		select {
		case result := <-clientResults:
			expected := bytes.Repeat([]byte{identities[result.index][0]}, 26_185)
			if result.err != nil {
				t.Fatal(result.err)
			}
			if !bytes.Equal(result.packet, expected) {
				t.Fatalf("tenant %d received another session's payload", result.index+1)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("clients did not receive tenant-specific payloads")
		}
	}
}
