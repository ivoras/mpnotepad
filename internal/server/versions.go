package server

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

func (s *Server) authorizedDoc(w http.ResponseWriter, r *http.Request) (string, bool) {
	docID := chi.URLParam(r, "id")
	doc, err := s.store.GetDocument(r.Context(), docID)
	if err != nil {
		s.log.Error("get document", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return "", false
	}
	if doc == nil {
		http.NotFound(w, r)
		return "", false
	}
	ok, err := HasAccess(r.Context(), s.store, doc, r)
	if err != nil {
		s.log.Error("access", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return "", false
	}
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return "", false
	}
	return docID, true
}

func (s *Server) handleVersionsList(w http.ResponseWriter, r *http.Request) {
	docID, ok := s.authorizedDoc(w, r)
	if !ok {
		return
	}
	versions, err := s.store.ListDocumentVersions(r.Context(), docID, 200)
	if err != nil {
		s.log.Error("list versions", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]map[string]any, 0, len(versions))
	for _, v := range versions {
		out = append(out, map[string]any{
			"id":         v.ID,
			"created_at": v.CreatedAt.Unix(),
			"char_count": v.CharCount,
			"byte_size":  v.ByteSize,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"versions": out})
}

func (s *Server) handleVersionGet(w http.ResponseWriter, r *http.Request) {
	docID, ok := s.authorizedDoc(w, r)
	if !ok {
		return
	}
	vidStr := chi.URLParam(r, "vid")
	vid, err := strconv.ParseInt(vidStr, 10, 64)
	if err != nil {
		http.Error(w, "bad version id", http.StatusBadRequest)
		return
	}
	v, err := s.store.GetDocumentVersion(r.Context(), docID, vid)
	if err != nil {
		s.log.Error("get version", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if v == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":         v.ID,
		"created_at": v.CreatedAt.Unix(),
		"text":       v.Text,
		"char_count": v.CharCount,
		"byte_size":  v.ByteSize,
	})
}
