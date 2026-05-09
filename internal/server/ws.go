package server

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	docID := chi.URLParam(r, "id")
	if docID == "" {
		http.NotFound(w, r)
		return
	}

	doc, err := s.store.GetDocument(r.Context(), docID)
	if err != nil {
		s.log.Error("get document", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if doc == nil {
		http.NotFound(w, r)
		return
	}

	ok, err := HasAccess(r.Context(), s.store, doc, r)
	if err != nil {
		s.log.Error("session", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	hub := s.acquireHub(docID, doc.YjsState)

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Warn("ws upgrade", "err", err)
		return
	}

	_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	})

	client := &wsClient{
		hub:  hub,
		conn: conn,
		send: make(chan []byte, 256),
	}
	hub.register <- client
	go client.writePump()
	client.readPump()
}
