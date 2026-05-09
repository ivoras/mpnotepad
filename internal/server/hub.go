package server

import (
	"context"
	"encoding/json"
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

	// latestText is the most recent flattened text snapshot pushed by any client.
	// hasText becomes true after the first text-snapshot is received.
	latestText string
	hasText    bool
	// lastSavedText is the text of the most recently persisted document_versions row;
	// loaded lazily on first save to avoid spamming duplicate snapshots across reconnects.
	lastSavedText    string
	lastSavedLoaded  bool

	// onEmpty is called (once, under hub goroutine) when the last client leaves.
	onEmpty func()
}

// frameKind distinguishes Yjs binary protocol frames from auxiliary text-snapshot frames.
type frameKind int

const (
	frameBinary frameKind = iota
	frameText
)

type clientFrame struct {
	c    *wsClient
	kind frameKind
	data []byte
}

type textSnapshotMsg struct {
	Type string `json:"type"`
	Text string `json:"text"`
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
			_ = h.snapshotVersion(context.Background())
			h.closeAllClients()
			return

		case c := <-h.register:
			h.clients[c] = struct{}{}

		case c := <-h.unregister:
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
				_ = h.snapshotVersion(context.Background())
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
	if frame.kind == frameText {
		var snap textSnapshotMsg
		if err := json.Unmarshal(frame.data, &snap); err != nil {
			h.log.Debug("text frame parse", "doc", h.docID, "err", err)
			return
		}
		if snap.Type == "text-snapshot" {
			h.latestText = snap.Text
			h.hasText = true
		}
		return
	}

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

// snapshotVersion writes the current flattened text as a new document_versions row,
// skipping the write if the text is identical to the most recently saved version.
// No-op if no text snapshot has been received yet.
func (h *Hub) snapshotVersion(ctx context.Context) error {
	if !h.hasText {
		return nil
	}
	if !h.lastSavedLoaded {
		prev, err := h.store.LatestDocumentVersionText(ctx, h.docID)
		if err != nil {
			h.log.Warn("load latest version", "doc", h.docID, "err", err)
		} else {
			h.lastSavedText = prev
			h.lastSavedLoaded = true
		}
	}
	if h.lastSavedLoaded && h.latestText == h.lastSavedText {
		return nil
	}
	if _, err := h.store.CreateDocumentVersion(ctx, h.docID, h.latestText); err != nil {
		h.log.Warn("create document version", "doc", h.docID, "err", err)
		return err
	}
	h.lastSavedText = h.latestText
	h.lastSavedLoaded = true
	return nil
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
		payload := append([]byte(nil), message...)
		switch mt {
		case websocket.BinaryMessage:
			c.hub.incoming <- clientFrame{c: c, kind: frameBinary, data: payload}
		case websocket.TextMessage:
			c.hub.incoming <- clientFrame{c: c, kind: frameText, data: payload}
		}
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
