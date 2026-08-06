package easytier

// DialMode describes how protocol services reach a remote EasyTier peer.
type DialMode uint8

const (
	// DialModeDirect reaches the peer through its virtual IP address.
	DialModeDirect DialMode = iota
	// DialModeProxy reaches the peer through a local static port forward.
	DialModeProxy
)
