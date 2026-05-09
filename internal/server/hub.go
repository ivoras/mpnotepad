package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/gorilla/websocket"

	"mpnotepad/internal/store"
)

// Hub relays Yjs binary frames between clients and persists opaque state.
type Hub struct {
	docID   string
	store   *store.Store
	log     *slog.Logger
	state   []byte
	clients map[*wsClient]struct{}

	register   chan *wsClient
	unregister chan *wsClient
	incoming   chan clientFrame

	dirty bool

	persistEvery time.Duration

	// onEmpty is called (once, under hub goroutine) when the last client leaves.
	onEmpty func()
}

type clientFrame struct {
	c    *wsClient
	data []byte
}

type wsClient struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
}

func newHub(log *slog.Logger, st *store.Store, docID string, initial []byte, onEmpty func()) *Hub {
	stCopy := append([]byte(nil), initial...)
	return &Hub{
		docID:        docID,
		store:        st,
		log:          log,
		state:        stCopy,
		clients:      make(map[*wsClient]struct{}),
		register:     make(chan *wsClient, 8),
		unregister:   make(chan *wsClient, 8),
		incoming:     make(chan clientFrame, 64),
		persistEvery: 5 * time.Second,
		onEmpty:      onEmpty,
	}
}

func (h *Hub) run(ctx context.Context) {
	ticker := time.NewTicker(h.persistEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			_ = h.flush(context.Background())
			h.closeAllClients()
			return

		case c := <-h.register:
			h.clients[c] = struct{}{}

		case c := <-h.unregister:
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
				if len(h.clients) == 0 {
					_ = h.flush(context.Background())
					if h.onEmpty != nil {
						h.onEmpty()
					}
					return
				}
			}

		case frame := <-h.incoming:
			h.handleFrame(frame)

		case <-ticker.C:
			if err := h.flush(context.Background()); err != nil {
				h.log.Warn("persist yjs state", "doc", h.docID, "err", err)
			}
		}
	}
}

func (h *Hub) closeAllClients() {
	for c := range h.clients {
		_ = c.conn.Close()
		close(c.send)
	}
	h.clients = make(map[*wsClient]struct{})
}

func (h *Hub) handleFrame(frame clientFrame) {
	msg := frame.data
	top, syncStep, inner, err := parseYjsWire(msg)
	if err != nil {
		h.log.Debug("yjs wire parse", "doc", h.docID, "err", err)
		return
	}

	switch top {
	case msgSync:
		switch syncStep {
		case syncStep1:
			reply := encodeSyncStep2(h.state)
			select {
			case frame.c.send <- reply:
			default:
				h.log.Warn("client send buffer full", "doc", h.docID)
			}
			return
		case syncStep2:
			if len(inner) > 0 {
				h.state = append([]byte(nil), inner...)
				h.dirty = true
			}
			h.broadcastExcept(frame.c, msg)
			return
		case syncUpdate:
			if len(inner) > 0 {
				h.state = mergeYjsState(h.state, inner)
				h.dirty = true
			}
			h.broadcastExcept(frame.c, msg)
			return
		default:
			h.broadcastExcept(frame.c, msg)
			return
		}
	case msgAwareness, msgQueryAwareness:
		h.broadcastExcept(frame.c, msg)
	default:
		// Unknown top-level: forward to peers (best effort).
		h.broadcastExcept(frame.c, msg)
	}
}

func (h *Hub) broadcastExcept(from *wsClient, msg []byte) {
	for c := range h.clients {
		if c == from {
			continue
		}
		select {
		case c.send <- append([]byte(nil), msg...):
		default:
			h.log.Warn("drop broadcast: slow client", "doc", h.docID)
		}
	}
}

func (h *Hub) flush(ctx context.Context) error {
	if !h.dirty {
		return nil
	}
	st := append([]byte(nil), h.state...)
	if err := h.store.UpdateYjsState(ctx, h.docID, st); err != nil {
		return err
	}
	h.dirty = false
	return nil
}

// Flush persists state if dirty (for process shutdown).
func (h *Hub) Flush(ctx context.Context) error {
	return h.flush(ctx)
}

func (c *wsClient) readPump() {
	defer func() {
		c.hub.unregister <- c
		_ = c.conn.Close()
	}()

	c.conn.SetReadLimit(1 << 20)
	_ = c.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	})

	for {
		mt, message, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		if mt != websocket.BinaryMessage {
			continue
		}
		payload := append([]byte(nil), message...)
		c.hub.incoming <- clientFrame{c: c, data: payload}
	}
}

func (c *wsClient) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.BinaryMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
