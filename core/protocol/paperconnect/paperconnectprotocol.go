package paperconnect

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	PCPing   = "c:ping"
	PCPlayer = "c:player"
)

type PCPingRequest struct {
	Time int64 `json:"time"`
}

type PCPingResponse struct {
	Time             int64  `json:"time"`
	ReturnTime       int64  `json:"returnTime"`
	GameType         string `json:"gameType"`
	GameProtocolType string `json:"gameProtocolType"`
	GamePort         int    `json:"gamePort"`
	Protocol         string `json:"protocol"` // "nethernet" or "raknet"
}

type PCPlayerRequest struct {
	ClientId   string `json:"clientId"`
	PlayerName string `json:"playerName"`
}

type PCPlayerEntry struct {
	PlayerName string `json:"player"`
	ClientId   string `json:"clientId"`
	IsRoomHost bool   `json:"isRoomHost"`

	lastHeartbeat time.Time
}

type PCPlayerResponse struct {
	ReturnTime int64           `json:"returnTime"`
	Players    []PCPlayerEntry `json:"players"`
}

type PCErrorResponse struct {
	Error string `json:"error"`
}

// WritePCRequest writes a namespace\0JSON request to the connection.
func WritePCRequest(conn net.Conn, namespace string, body interface{}) error {
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

	jsonBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Build payload: namespace + 0x00 + JSON
	payload := make([]byte, 0, len(namespace)+1+len(jsonBytes))
	payload = append(payload, namespace...)
	payload = append(payload, 0)
	payload = append(payload, jsonBytes...)

	_, err = conn.Write(payload)
	return err
}

// WritePCResponse writes a JSON response to the connection.
// In the PaperConnect protocol, responses are just raw JSON (no framing).
func WritePCResponse(conn net.Conn, response interface{}) error {
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

	jsonBytes, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("failed to marshal response: %w", err)
	}

	_, err = conn.Write(jsonBytes)
	return err
}

// WritePCError writes an error response to the connection.
func WritePCError(conn net.Conn, message string) error {
	return WritePCResponse(conn, PCErrorResponse{Error: message})
}

// ReadPCRequest reads a full namespace\0JSON request from the connection.
// It reads data in chunks until the null byte is found (for namespace),
// then reads the remaining JSON body.
func ReadPCRequest(conn net.Conn) (namespace string, rawJson []byte, err error) {
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	// Read a chunk of data. The C# server reads up to 4096 bytes.
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read request: %w", err)
	}
	if n == 0 {
		return "", nil, fmt.Errorf("empty request")
	}

	data := buf[:n]

	// Find the null byte separator
	nullIdx := -1
	for i, b := range data {
		if b == 0 {
			nullIdx = i
			break
		}
	}
	if nullIdx == -1 {
		return "", nil, fmt.Errorf("invalid request: no null byte separator found")
	}

	namespace = string(data[:nullIdx])
	rawJson = data[nullIdx+1:]

	return namespace, rawJson, nil
}

// ReadPCResponse reads a JSON response from the connection.
func ReadPCResponse(conn net.Conn) (rawJson []byte, err error) {
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return buf[:n], nil
}

// Protocol constants.
const (
	ProtocolNetherNet = "nethernet"
	ProtocolRakNet    = "raknet"
)

// Hostname encoding:
//   NetherNet: paper-connect-server-{tcpPort}
//   RakNet:   paper-connect-server-{tcpPort}-r-{gamePort}

const hostnameRakNetMarker = "-r-"

func buildHostname(tcpPort uint16) string {
	return fmt.Sprintf("%s%d", pcHostnamePrefix, tcpPort)
}

func buildHostnameRakNet(tcpPort uint16, gamePort uint16) string {
	return fmt.Sprintf("%s%d%s%d", pcHostnamePrefix, tcpPort, hostnameRakNetMarker, gamePort)
}

// ParsedHostname holds values decoded from an EasyTier peer hostname.
type ParsedHostname struct {
	TCPPort  uint16
	Protocol string // ProtocolNetherNet or ProtocolRakNet
	GamePort uint16 // Only valid when Protocol == ProtocolRakNet
}

func parseHostname(hostname string) (*ParsedHostname, error) {
	rest := strings.TrimPrefix(hostname, pcHostnamePrefix)
	if rest == hostname {
		return nil, fmt.Errorf("hostname missing prefix %q", pcHostnamePrefix)
	}
	if idx := strings.Index(rest, hostnameRakNetMarker); idx != -1 {
		tcpStr := rest[:idx]
		gameStr := rest[idx+len(hostnameRakNetMarker):]
		tcpPort, err := strconv.ParseUint(tcpStr, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("invalid TCP port in hostname: %s", tcpStr)
		}
		gamePort, err := strconv.ParseUint(gameStr, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("invalid game port in hostname: %s", gameStr)
		}
		return &ParsedHostname{TCPPort: uint16(tcpPort), Protocol: ProtocolRakNet, GamePort: uint16(gamePort)}, nil
	}
	tcpPort, err := strconv.ParseUint(rest, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("invalid TCP port in hostname: %s", rest)
	}
	return &ParsedHostname{TCPPort: uint16(tcpPort), Protocol: ProtocolNetherNet}, nil
}
