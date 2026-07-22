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
	if err := writeTunnelPacket(client, payload); err != nil {
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
		if err := writeTunnelPacket(clients[i], identities[i]); err != nil {
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
			serverErrs <- writeTunnelPacket(conn, payload)
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
