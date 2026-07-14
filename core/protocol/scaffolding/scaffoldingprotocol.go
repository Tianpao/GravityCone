package scaffolding

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

var ErrMCProxyConnection = errors.New("mc proxy connection")

const (
	ProtocolPing               = "c:ping"
	ProtocolProtocols          = "c:protocols"
	ProtocolServerPort         = "c:server_port"
	ProtocolPlayerPing         = "c:player_ping"
	ProtocolPlayerEasyTierID   = "c:player_easytier_id"
	ProtocolPlayerProfilesList = "c:player_profiles_list"
)

const (
	StatusOK               = 0
	StatusUnknownProtocol  = 1
	StatusBadRequest       = 2
	StatusServerNotStarted = 32
	StatusUnknownError     = 255
)

type PlayerInfo struct {
	Name       string `json:"name"`
	MachineID  string `json:"machine_id"`
	EasyTierID string `json:"easytier_id,omitempty"`
	Vendor     string `json:"vendor"`
	Kind       string `json:"kind"` // "HOST" or "GUEST"
}

func ReadProtocolRequest(conn net.Conn) (typeName string, body []byte, err error) {
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	var typeLenBuf [1]byte
	if _, err := io.ReadFull(conn, typeLenBuf[:]); err != nil {
		return "", nil, fmt.Errorf("读取请求类型长度失败: %w", err)
	}
	typeLen := int(typeLenBuf[0])
	if typeLen == 0 {
		return "", nil, ErrMCProxyConnection
	}

	typeBuf := make([]byte, typeLen)
	if _, err := io.ReadFull(conn, typeBuf); err != nil {
		return "", nil, fmt.Errorf("读取请求类型失败: %w", err)
	}

	var bodyLenBuf [4]byte
	if _, err := io.ReadFull(conn, bodyLenBuf[:]); err != nil {
		return "", nil, fmt.Errorf("读取请求体长度失败: %w", err)
	}
	bodyLen := int(binary.BigEndian.Uint32(bodyLenBuf[:]))

	if bodyLen > 1024*1024 { // 1MB max
		return "", nil, fmt.Errorf("请求体过大: %d bytes", bodyLen)
	}

	body = make([]byte, bodyLen)
	if bodyLen > 0 {
		if _, err := io.ReadFull(conn, body); err != nil {
			return "", nil, fmt.Errorf("读取请求体失败: %w", err)
		}
	}

	return string(typeBuf), body, nil
}

func WriteProtocolRequest(conn net.Conn, typeName string, body []byte) error {
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

	typeBytes := []byte(typeName)
	if len(typeBytes) > 255 {
		return fmt.Errorf("请求类型过长: %d", len(typeBytes))
	}

	buf := make([]byte, 0, 1+len(typeBytes)+4+len(body))
	buf = append(buf, byte(len(typeBytes)))
	buf = append(buf, typeBytes...)
	bodyLen := make([]byte, 4)
	binary.BigEndian.PutUint32(bodyLen, uint32(len(body)))
	buf = append(buf, bodyLen...)
	buf = append(buf, body...)

	_, err := conn.Write(buf)
	return err
}

func ReadProtocolResponse(conn net.Conn) (status uint8, body []byte, err error) {
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	var header [5]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return 0, nil, fmt.Errorf("读取响应头失败: %w", err)
	}

	status = header[0]
	bodyLen := int(binary.BigEndian.Uint32(header[1:5]))

	if bodyLen > 1024*1024 {
		return status, nil, fmt.Errorf("响应体过大: %d bytes", bodyLen)
	}

	body = make([]byte, bodyLen)
	if bodyLen > 0 {
		if _, err := io.ReadFull(conn, body); err != nil {
			return status, nil, fmt.Errorf("读取响应体失败: %w", err)
		}
	}

	return status, body, nil
}

func WriteProtocolResponse(conn net.Conn, status uint8, body []byte) error {
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

	header := [5]byte{}
	header[0] = status
	binary.BigEndian.PutUint32(header[1:5], uint32(len(body)))

	if _, err := conn.Write(header[:]); err != nil {
		return err
	}
	if len(body) > 0 {
		_, err := conn.Write(body)
		return err
	}
	return nil
}
