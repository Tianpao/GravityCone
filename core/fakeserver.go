package core

import (
	"fmt"
	"log/slog"
	"net"
	"time"

	"golang.org/x/net/ipv4"
)

// FakeServer broadcasts a Minecraft LAN discovery packet on the local network
// so that Minecraft clients on the same LAN can see the proxied server in
// their multiplayer server list.
//
// It sends UDP multicast packets to 224.0.2.60:4445 (the standard Minecraft
// LAN discovery endpoint) every 1.5 seconds with the format:
//
//	[MOTD]{motd}[/MOTD][AD]{port}[/AD]
type FakeServer struct {
	stopCh chan struct{}
}

// NewFakeServer creates and starts a FakeServer that broadcasts the given
// port and MOTD on the local network. Call Stop to terminate the broadcast.
func NewFakeServer(port uint16, motd string) *FakeServer {
	fs := &FakeServer{stopCh: make(chan struct{})}
	go fs.run(port, motd)
	return fs
}

// Stop terminates the broadcast goroutine.
func (fs *FakeServer) Stop() {
	select {
	case <-fs.stopCh:
	default:
		close(fs.stopCh)
	}
}

type fakeServerConn struct {
	conn *net.UDPConn
	pconn *ipv4.PacketConn
}

func (fs *FakeServer) run(port uint16, motd string) {
	group := &net.UDPAddr{IP: net.IPv4(224, 0, 2, 60), Port: 4445}

	var conns []fakeServerConn

	ifaces, err := net.Interfaces()
	if err != nil {
		slog.Warn("failed to list interfaces", "error", err)
		return
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.To4() == nil {
				continue
			}
			ip := ipNet.IP.To4()

			// Skip loopback and EasyTier virtual network addresses
			if ip.IsLoopback() || isEasyTierIP(ip) {
				continue
			}

			bindAddr := &net.UDPAddr{IP: ip, Port: 0}
			conn, err := net.ListenPacket("udp4", bindAddr.String())
			if err != nil {
					slog.Warn("bind failed", "ip", ip, "error", err)
				continue
			}
			udpConn := conn.(*net.UDPConn)
			pconn := ipv4.NewPacketConn(udpConn)
			pconn.SetMulticastTTL(4)
			pconn.SetMulticastLoopback(true)

			conns = append(conns, fakeServerConn{conn: udpConn, pconn: pconn})
		}
	}

	// Fallback: if no interface-bound sockets, bind to 0.0.0.0
	if len(conns) == 0 {
		conn, err := net.ListenPacket("udp4", "0.0.0.0:0")
		if err != nil {
			slog.Warn("fallback bind failed", "error", err)
			return
		}
		udpConn := conn.(*net.UDPConn)
		pconn := ipv4.NewPacketConn(udpConn)
		pconn.SetMulticastTTL(4)
		pconn.SetMulticastLoopback(true)
		conns = append(conns, fakeServerConn{conn: udpConn, pconn: pconn})
	}

	message := fmt.Sprintf("[MOTD]%s[/MOTD][AD]%d[/AD]", motd, port)
	data := []byte(message)

	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()

	defer func() {
		for _, c := range conns {
			c.pconn.Close()
			c.conn.Close()
		}
	}()

	slog.Info("FakeServer started broadcasting", "port", port, "group", group)
	for {
		select {
		case <-fs.stopCh:
			slog.Info("FakeServer stopped")
			return
		case <-ticker.C:
			for _, c := range conns {
				c.conn.WriteToUDP(data, group)
			}
		}
	}
}

// isEasyTierIP checks if an IP belongs to the EasyTier virtual network range.
func isEasyTierIP(ip net.IP) bool {
	// EasyTier uses 10.144.144.0/24 by default
	if ip[0] == 10 && ip[1] == 144 && ip[2] == 144 {
		return true
	}
	return false
}
