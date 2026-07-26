package paperconnect

import (
	"encoding/binary"
	"fmt"
	"math"
	"sync"
	"sync/atomic"

	raknet "github.com/sandertv/go-raknet"
)

const rakNetMTU = 1400

// tunnelChunkSize is the maximum payload size per tunnel chunk frame.
// With RakNet MTU=1400: effectiveMTU=1372, maxSize=1358 (no internal split),
// chunk frame overhead=9 bytes, so max payload=1349.
const tunnelChunkSize = 1349

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

// tunnelFramePool recycles byte slices used for tunnel chunk frames.
// go-raknet's Conn.Write copies data into its own internal buffer,
// so the caller's slice is not referenced after Write returns.
var tunnelFramePool = sync.Pool{
	New: func() any {
		b := make([]byte, 9+tunnelChunkSize)
		return &b
	},
}

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
		chunkLen := end - start

		framePtr := tunnelFramePool.Get().(*[]byte)
		if cap(*framePtr) < 9+chunkLen {
			*framePtr = make([]byte, 9+chunkLen)
		}
		frame := (*framePtr)[:9+chunkLen]

		frame[0] = tunnelChunk
		binary.BigEndian.PutUint32(frame[1:], messageID)
		binary.BigEndian.PutUint16(frame[5:], uint16(chunkCount))
		binary.BigEndian.PutUint16(frame[7:], uint16(i))
		copy(frame[9:], packet[start:end])

		_, err := conn.Write(frame)
		tunnelFramePool.Put(framePtr)
		if err != nil {
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
			return frame[5:], nil
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
			parts[index] = frame[9:]
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
					r.ids[i] = r.ids[len(r.ids)-1]
					r.ids = r.ids[:len(r.ids)-1]
					break
				}
			}
			// Pre-allocate result buffer and copy parts into it.
			totalSize := 0
			for _, part := range parts {
				totalSize += len(part)
			}
			result := make([]byte, totalSize)
			offset := 0
			for _, part := range parts {
				offset += copy(result[offset:], part)
			}
			return result, nil
		default:
			return nil, fmt.Errorf("unknown tunnel frame type %d", frame[0])
		}
	}
}
