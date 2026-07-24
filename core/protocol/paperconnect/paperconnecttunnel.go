package paperconnect

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"sync/atomic"

	raknet "github.com/sandertv/go-raknet"
)

const rakNetMTU = 1200

const tunnelChunkSize = 900

const maxTunnelChunks = 1024

const (
	tunnelPacket byte = iota + 1
	tunnelChunk
)

type tunnelReader struct {
	conn   *raknet.Conn
	chunks map[uint32][][]byte
	ids    []uint32 // insertion-order for eviction
}

var tunnelMessageID atomic.Uint32

func newTunnelReader(conn *raknet.Conn) *tunnelReader {
	return &tunnelReader{conn: conn, chunks: make(map[uint32][][]byte), ids: make([]uint32, 0)}
}

func writeTunnelPacket(conn *raknet.Conn, packet []byte) error {
	if len(packet) > math.MaxUint32 {
		return fmt.Errorf("packet is too large: %d bytes", len(packet))
	}
	if len(packet) <= tunnelChunkSize {
		frame := make([]byte, 1+4+len(packet))
		frame[0] = tunnelPacket
		binary.BigEndian.PutUint32(frame[1:], uint32(len(packet)))
		copy(frame[5:], packet)
		_, err := conn.Write(frame)
		return err
	}

	chunkCount := (len(packet) + tunnelChunkSize - 1) / tunnelChunkSize
	messageID := tunnelMessageID.Add(1)
	for i := 0; i < chunkCount; i++ {
		start := i * tunnelChunkSize
		end := min(start+tunnelChunkSize, len(packet))
		frame := make([]byte, 1+4+2+2+len(packet[start:end]))
		frame[0] = tunnelChunk
		binary.BigEndian.PutUint32(frame[1:], messageID)
		binary.BigEndian.PutUint16(frame[5:], uint16(chunkCount))
		binary.BigEndian.PutUint16(frame[7:], uint16(i))
		copy(frame[9:], packet[start:end])
		if _, err := conn.Write(frame); err != nil {
			return err
		}
	}
	return nil
}

func (r *tunnelReader) ReadPacket() ([]byte, error) {
	for {
		frame, err := r.conn.ReadPacket()
		if err != nil {
			return nil, err
		}
		if len(frame) < 1 {
			return nil, fmt.Errorf("invalid empty tunnel frame")
		}
		switch frame[0] {
		case tunnelPacket:
			if len(frame) < 5 {
				return nil, fmt.Errorf("invalid tunnel packet frame")
			}
			length := int(binary.BigEndian.Uint32(frame[1:]))
			if length != len(frame)-5 {
				return nil, fmt.Errorf("invalid tunnel packet length: got %d, expected %d", len(frame)-5, length)
			}
			return bytes.Clone(frame[5:]), nil
		case tunnelChunk:
			if len(frame) < 9 {
				return nil, fmt.Errorf("invalid tunnel chunk frame")
			}
			messageID := binary.BigEndian.Uint32(frame[1:])
			count := int(binary.BigEndian.Uint16(frame[5:]))
			index := int(binary.BigEndian.Uint16(frame[7:]))
			if count == 0 || index >= count {
				return nil, fmt.Errorf("invalid tunnel chunk index %d of %d", index, count)
			}
			parts := r.chunks[messageID]
			if parts == nil {
				if len(r.chunks) >= maxTunnelChunks {
					evictID := r.ids[0]
					r.ids = r.ids[1:]
					delete(r.chunks, evictID)
				}
				parts = make([][]byte, count)
				r.chunks[messageID] = parts
				r.ids = append(r.ids, messageID)
			}
			if len(parts) != count {
				return nil, fmt.Errorf("inconsistent tunnel chunk count")
			}
			parts[index] = bytes.Clone(frame[9:])
			complete := true
			for _, part := range parts {
				if part == nil {
					complete = false
					break
				}
			}
			if !complete {
				continue
			}
			delete(r.chunks, messageID)
				for i, id := range r.ids {
					if id == messageID {
						r.ids = append(r.ids[:i], r.ids[i+1:]...)
						break
					}
				}
			return bytes.Join(parts, nil), nil
		default:
			return nil, fmt.Errorf("unknown tunnel frame type %d", frame[0])
		}
	}
}
