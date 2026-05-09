package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/gorilla/websocket"

	"mpnotepad/internal/store"
)

// Hub relays Yjs binary frames between clients and persists state as an ordered
// list of individual Yjs updates (each one decodable by a single Y.applyUpdate
// call on the client). Concatenating updates into one buffer would be wrong:
// Y.applyUpdate decodes exactly one update at a time and stops, so naive
// concatenation silently drops everything after the first edit.
type Hub struct {
	docID   string
	store   *store.Store
	log     *slog.Logger
	updates [][]byte
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

// safeSnapshotShrinkRatio guards against accidentally overwriting history with
// a much smaller snapshot from a client that didn't receive the initial Yjs
// sync. A client may submit shorter text only if it's at least this fraction
// of the previously-saved version, OR if the previous version was empty.
const safeSnapshotShrinkRatio = 0.5

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
	return &Hub{
		docID:        docID,
		store:        st,
		log:          log,
		updates:      parseStoredState(initial),
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
			// Point-to-point: only the requester needs the response. Send the
			// first stored update as Sync.Step2 (the protocol-mandated reply
			// to Step1) and the rest as separate Sync.Update messages, since
			// a single Step2 buffer can only carry one decodable update.
			h.sendInitialSync(frame.c)
			return
		case syncStep2:
			// A client's step2 carries updates the server should integrate.
			// Append (don't replace) so updates from other clients aren't lost.
			// Re-broadcast as an Update so peers integrate via the additive
			// path (step2 is point-to-point in the y-protocols spec).
			if len(inner) > 0 {
				h.appendUpdate(inner)
				h.broadcastExcept(frame.c, encodeSyncUpdate(inner))
			}
			return
		case syncUpdate:
			if len(inner) > 0 {
				h.appendUpdate(inner)
				h.broadcastExcept(frame.c, msg)
			}
			return
		default:
			// Unknown sync sub-step: forward as-is (best effort).
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
	blob := encodeStoredState(h.updates)
	if err := h.store.UpdateYjsState(ctx, h.docID, blob); err != nil {
		return err
	}
	h.dirty = false
	return nil
}

// appendUpdate stores a copy of one Yjs update and marks the hub dirty.
func (h *Hub) appendUpdate(update []byte) {
	h.updates = append(h.updates, append([]byte(nil), update...))
	h.dirty = true
}

// sendInitialSync delivers the full document state to a single client as the
// reply to its Sync.Step1: first stored update as Sync.Step2, remainder as
// individual Sync.Update messages. This is the only correct way to deliver
// multiple Yjs updates over the wire — Y.applyUpdate decodes one update per
// message, never multiple from one buffer.
func (h *Hub) sendInitialSync(c *wsClient) {
	if len(h.updates) == 0 {
		h.deliver(c, encodeSyncStep2(nil))
		return
	}
	if !h.deliver(c, encodeSyncStep2(h.updates[0])) {
		return
	}
	for _, u := range h.updates[1:] {
		if !h.deliver(c, encodeSyncUpdate(u)) {
			return
		}
	}
}

// deliver enqueues one already-encoded message to a single client. Returns
// false if the client's send buffer is full (we drop and warn rather than
// block the hub goroutine).
func (h *Hub) deliver(c *wsClient, msg []byte) bool {
	select {
	case c.send <- msg:
		return true
	default:
		h.log.Warn("client send buffer full", "doc", h.docID)
		return false
	}
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
	// Refuse to persist a snapshot that is suspiciously smaller than the last
	// known good text — almost certainly a client whose Yjs sync silently failed.
	if h.lastSavedLoaded && len(h.lastSavedText) > 0 {
		if float64(len(h.latestText)) < safeSnapshotShrinkRatio*float64(len(h.lastSavedText)) {
			h.log.Warn("rejecting suspicious snapshot",
				"doc", h.docID,
				"prev_len", len(h.lastSavedText),
				"new_len", len(h.latestText))
			return nil
		}
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
