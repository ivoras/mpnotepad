package server

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// y-websocket / y-protocols wire format (lib0 varuint + varbytes).
// Top-level: messageSync=0, messageAwareness=1, messageQueryAwareness=3
// Nested sync: SyncStep1=0, SyncStep2=1, Update=2

const (
	msgSync           = 0
	msgAwareness      = 1
	msgQueryAwareness = 3
	syncStep1         = 0
	syncStep2         = 1
	syncUpdate        = 2
)

func appendVarUint(buf []byte, v uint64) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v)|0x80)
		v >>= 7
	}
	return append(buf, byte(v))
}

func appendVarBytes(buf, data []byte) []byte {
	buf = appendVarUint(buf, uint64(len(data)))
	return append(buf, data...)
}

func readVarUint(data []byte, off int) (val uint64, n int, err error) {
	var s uint
	for i := off; i < len(data); i++ {
		b := data[i]
		val |= uint64(b&0x7f) << s
		if b < 0x80 {
			return val, i - off + 1, nil
		}
		s += 7
		if s >= 64 {
			return 0, 0, errors.New("varuint overflow")
		}
	}
	return 0, 0, errors.New("truncated varuint")
}

func readVarBytes(data []byte, off int) (payload []byte, n int, err error) {
	l, vn, err := readVarUint(data, off)
	if err != nil {
		return nil, 0, err
	}
	off += vn
	if l > uint64(len(data)-off) {
		return nil, 0, fmt.Errorf("varbytes length %d exceeds remainder", l)
	}
	end := off + int(l)
	return data[off:end], vn + int(l), nil
}

// encodeSyncStep2 builds [msgSync][syncStep2][state] message.
func encodeSyncStep2(state []byte) []byte {
	var buf []byte
	buf = appendVarUint(buf, msgSync)
	buf = appendVarUint(buf, syncStep2)
	buf = appendVarBytes(buf, state)
	return buf
}

// encodeSyncUpdate builds [msgSync][syncUpdate][update] message.
// Used to re-broadcast peer-supplied updates (or to project a peer's step2
// payload to other clients, since step2 is point-to-point).
func encodeSyncUpdate(update []byte) []byte {
	var buf []byte
	buf = appendVarUint(buf, msgSync)
	buf = appendVarUint(buf, syncUpdate)
	buf = appendVarBytes(buf, update)
	return buf
}

// parseYjsWire inspects a y-websocket binary frame. If it is a nested sync message,
// returns syncStep and the inner document payload (for step2/update); payload is nil for step1.
func parseYjsWire(msg []byte) (top uint64, syncStep int, inner []byte, err error) {
	if len(msg) == 0 {
		return 0, -1, nil, errors.New("empty message")
	}
	t, n0, err := readVarUint(msg, 0)
	if err != nil {
		return 0, -1, nil, err
	}
	off := n0
	switch t {
	case msgSync:
		step, n1, err := readVarUint(msg, off)
		if err != nil {
			return t, -1, nil, err
		}
		off += n1
		switch step {
		case syncStep1:
			// remainder is state vector; not used by relay
			return t, syncStep1, nil, nil
		case syncStep2, syncUpdate:
			inner, n2, err := readVarBytes(msg, off)
			if err != nil {
				return t, int(step), nil, err
			}
			_ = n2
			return t, int(step), inner, nil
		default:
			return t, int(step), nil, nil
		}
	default:
		return t, -1, nil, nil
	}
}

// Persistence framing for the list of Yjs updates that make up a document's
// state. Yjs's Y.applyUpdate decodes exactly one update from a buffer; naive
// byte concatenation is therefore NOT a valid merge — the second and later
// updates would be silently dropped. We store each update separately,
// length-prefixed, behind a magic header so existing legacy blobs (which
// were a single update or a corrupt concat) can still be parsed.
const stateMagicV1 = "MPNV1\n"

// parseStoredState parses a persisted yjs_state blob into an ordered list of
// individual Yjs updates. Empty input → empty slice. Legacy blobs without
// the magic header are returned as a single update so the first edit's text
// is at least preserved (older revisions of this server concatenated raw
// update bytes; only the first one survives Y.applyUpdate decoding).
func parseStoredState(b []byte) [][]byte {
	if len(b) == 0 {
		return nil
	}
	if !bytes.HasPrefix(b, []byte(stateMagicV1)) {
		return [][]byte{append([]byte(nil), b...)}
	}
	body := b[len(stateMagicV1):]
	var updates [][]byte
	for off := 0; off+4 <= len(body); {
		n := int(binary.BigEndian.Uint32(body[off : off+4]))
		off += 4
		if n < 0 || off+n > len(body) {
			break
		}
		updates = append(updates, append([]byte(nil), body[off:off+n]...))
		off += n
	}
	return updates
}

// encodeStoredState serializes the updates list back into the on-disk blob.
func encodeStoredState(updates [][]byte) []byte {
	total := len(stateMagicV1)
	for _, u := range updates {
		total += 4 + len(u)
	}
	out := make([]byte, 0, total)
	out = append(out, stateMagicV1...)
	var sz [4]byte
	for _, u := range updates {
		binary.BigEndian.PutUint32(sz[:], uint32(len(u)))
		out = append(out, sz[:]...)
		out = append(out, u...)
	}
	return out
}
