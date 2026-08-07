package paperconnect

import (
	"encoding/binary"
	"fmt"
	"math"
	"sync"

	raknet "github.com/sandertv/go-raknet"
)

const rakNetMTU = 1400

// tunnelChunkSize is the maximum payload size per tunnel chunk frame.
// With RakNet MTU=1400: effectiveMTU=1372, maxSize=1358 (no internal split),
// chunk frame overhead=9 bytes, so max payload=1349.
const tunnelChunkSize = 1349

// tunnelBulkThreshold is the packet size above which a tunnel message is sent
// with reliable-but-unordered reliability (WriteUnordered) instead of the
// ordered reliability used for ordinary gameplay packets. Bulk payloads above
// this size (e.g. Bedrock resource pack chunks, 128KB-1MB, and large level
// chunks) tolerate reordering because the receiving client reassembles them by
// index/coordinate, so they are the ones that suffer from RakNet's ordered
// queue head-of-line blocking under packet loss. Small packets stay on the
// ordered path and are delivered exactly as before.
const tunnelBulkThreshold = 64 * 1024

const maxTunnelChunks = 1024

const (
	tunnelPacket byte = iota + 1
	tunnelChunk
)

// tunnelWriter sends logical packets over a single RakNet connection. It
// assigns each message a per-connection sequence number that the peer's
// tunnelReader uses to deliver messages in order, even when chunks are sent
// with reliable-unordered reliability and arrive out of order.
type tunnelWriter struct {
	conn *raknet.Conn
	seq  uint32
}

// tunnelReader reassembles tunnel messages and delivers them in the order they
// were sent (by sequence number). Chunks of a message may arrive out of order
// when sent with reliable-unordered reliability; the reader rebuilds each
// message from its (seq, index) parts and holds back any message until all
// earlier sequences have been delivered, preserving cross-message ordering.
type tunnelReader struct {
	conn    *raknet.Conn
	next    uint32                       // next sequence expected for delivery
	pending map[uint32]*pendingTunnelMsg // incomplete or not-yet-delivered messages
	ids     []uint32                     // insertion-order of pending keys for eviction
	ready   [][]byte                     // in-order messages awaiting return by ReadPacket
}

// pendingTunnelMsg accumulates the parts of a single tunnel message until it
// is complete, after which it is delivered in sequence order.
type pendingTunnelMsg struct {
	chunks   [][]byte // non-nil slots hold received chunks (large messages)
	count    int      // total chunk count for large messages
	content  []byte   // single-frame payload for small messages
	complete bool
}

// tunnelFramePool recycles byte slices used for tunnel chunk frames.
// go-raknet's Conn.Write copies data into its own internal buffer,
// so the caller's slice is not referenced after Write returns.
var tunnelFramePool = sync.Pool{
	New: func() any {
		b := make([]byte, 9+tunnelChunkSize)
		return &b
	},
}

func newTunnelWriter(conn *raknet.Conn) *tunnelWriter {
	return &tunnelWriter{conn: conn}
}

func newTunnelReader(conn *raknet.Conn) *tunnelReader {
	// A fresh tunnelWriter assigns seq 1 to its first message, so the reader
	// starts expecting seq 1.
	return &tunnelReader{conn: conn, next: 1, pending: make(map[uint32]*pendingTunnelMsg)}
}

// Write sends a single logical packet over the tunnel. Packets larger than
// tunnelBulkThreshold are sent with reliable-unordered reliability so a lost
// chunk does not block delivery of the peer's subsequent messages; all other
// packets use the ordinary reliable-ordered path.
func (w *tunnelWriter) Write(packet []byte) error {
	if len(packet) > math.MaxUint32 {
		return fmt.Errorf("packet is too large: %d bytes", len(packet))
	}

	w.seq++
	seq := w.seq

	if len(packet) <= tunnelChunkSize {
		frame := make([]byte, 1+4+4+len(packet))
		frame[0] = tunnelPacket
		binary.BigEndian.PutUint32(frame[1:], seq)
		binary.BigEndian.PutUint32(frame[5:], uint32(len(packet)))
		copy(frame[9:], packet)
		_, err := w.conn.Write(frame)
		return err
	}

	chunkCount := (len(packet) + tunnelChunkSize - 1) / tunnelChunkSize
	unordered := len(packet) > tunnelBulkThreshold
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
		binary.BigEndian.PutUint32(frame[1:], seq)
		binary.BigEndian.PutUint16(frame[5:], uint16(chunkCount))
		binary.BigEndian.PutUint16(frame[7:], uint16(i))
		copy(frame[9:], packet[start:end])

		var err error
		if unordered {
			_, err = w.conn.WriteUnordered(frame)
		} else {
			_, err = w.conn.Write(frame)
		}
		tunnelFramePool.Put(framePtr)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *tunnelReader) ReadPacket() ([]byte, error) {
	for {
		if len(r.ready) > 0 {
			pk := r.ready[0]
			r.ready = r.ready[1:]
			return pk, nil
		}

		frame, err := r.conn.ReadPacket()
		if err != nil {
			return nil, err
		}
		if len(frame) < 1 {
			return nil, fmt.Errorf("invalid empty tunnel frame")
		}
		switch frame[0] {
		case tunnelPacket:
			if len(frame) < 9 {
				return nil, fmt.Errorf("invalid tunnel packet frame")
			}
			seq := binary.BigEndian.Uint32(frame[1:])
			length := int(binary.BigEndian.Uint32(frame[5:]))
			if length != len(frame)-9 {
				return nil, fmt.Errorf("invalid tunnel packet length: got %d, expected %d", len(frame)-9, length)
			}
			content := make([]byte, length)
			copy(content, frame[9:])
			r.store(seq, &pendingTunnelMsg{content: content, complete: true})
		case tunnelChunk:
			if len(frame) < 9 {
				return nil, fmt.Errorf("invalid tunnel chunk frame")
			}
			seq := binary.BigEndian.Uint32(frame[1:])
			count := int(binary.BigEndian.Uint16(frame[5:]))
			index := int(binary.BigEndian.Uint16(frame[7:]))
			if count == 0 || index >= count {
				return nil, fmt.Errorf("invalid tunnel chunk index %d of %d", index, count)
			}
			msg := r.pending[seq]
			if msg == nil {
				msg = r.store(seq, &pendingTunnelMsg{count: count, chunks: make([][]byte, count)})
			}
			if msg.count != count {
				return nil, fmt.Errorf("inconsistent tunnel chunk count")
			}
			msg.chunks[index] = frame[9:]
			complete := true
			for _, part := range msg.chunks {
				if part == nil {
					complete = false
					break
				}
			}
			if complete {
				totalSize := 0
				for _, part := range msg.chunks {
					totalSize += len(part)
				}
				result := make([]byte, totalSize)
				offset := 0
				for _, part := range msg.chunks {
					offset += copy(result[offset:], part)
				}
				msg.content = result
				msg.chunks = nil
				msg.complete = true
			}
		default:
			return nil, fmt.Errorf("unknown tunnel frame type %d", frame[0])
		}

		r.drainReady()
	}
}

// store registers msg under seq, evicting the oldest pending entry if the
// pending map exceeds its capacity. It returns the stored entry.
func (r *tunnelReader) store(seq uint32, msg *pendingTunnelMsg) *pendingTunnelMsg {
	if _, ok := r.pending[seq]; !ok {
		if len(r.pending) >= maxTunnelChunks {
			evictID := r.ids[0]
			r.ids = r.ids[1:]
			delete(r.pending, evictID)
		}
		r.pending[seq] = msg
		r.ids = append(r.ids, seq)
		return msg
	}
	return r.pending[seq]
}

// drainReady moves every consecutive in-order complete message into ready so
// ReadPacket can return them without waiting for further inbound frames.
func (r *tunnelReader) drainReady() {
	for {
		msg, ok := r.pending[r.next]
		if !ok || !msg.complete {
			return
		}
		delete(r.pending, r.next)
		for i, id := range r.ids {
			if id == r.next {
				r.ids[i] = r.ids[len(r.ids)-1]
				r.ids = r.ids[:len(r.ids)-1]
				break
			}
		}
		r.next++
		r.ready = append(r.ready, msg.content)
	}
}