package server

import (
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

// mergeYjsState appends an update payload to existing state (Yjs accepts concatenated updates).
func mergeYjsState(existing, update []byte) []byte {
	if len(existing) == 0 {
		return append([]byte(nil), update...)
	}
	out := make([]byte, 0, len(existing)+len(update))
	out = append(out, existing...)
	out = append(out, update...)
	return out
}
