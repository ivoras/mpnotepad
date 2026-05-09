package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"mpnotepad/internal/ulidgen"
)

type pageData struct {
	Title           string
	DocID           string
	HasMarkdownPane bool
	DocTitle        string
	AuthError       bool
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	if err := s.templates.ExecuteTemplate(w, "home.html", pageData{Title: "Multiplayer Notepad"}); err != nil {
		s.log.Error("template home", "err", err)
	}
}

func (s *Server) handleNewDoc(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := ulidgen.New()
	if err := s.store.CreateDocument(r.Context(), id, "", nil, true); err != nil {
		s.log.Error("create doc", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	tok, err := NewSessionToken()
	if err != nil {
		s.log.Error("session token", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	expires := time.Now().Add(s.cfg.SessionTTL)
	if err := s.store.CreateSession(r.Context(), tok, id, expires); err != nil {
		s.log.Error("session create", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	maxAge := int(s.cfg.SessionTTL.Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	setSessionCookie(w, id, tok, maxAge)
	http.Redirect(w, r, docPathPrefix(id), http.StatusSeeOther)
}

func (s *Server) handleDocView(w http.ResponseWriter, r *http.Request) {
	docID := chi.URLParam(r, "id")
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
		s.log.Error("access", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !ok {
		if err := s.templates.ExecuteTemplate(w, "password.html", pageData{Title: "Unlock document", DocID: docID}); err != nil {
			s.log.Error("template password", "err", err)
		}
		return
	}

	data := pageData{
		Title:           doc.Title,
		DocID:           doc.ID,
		HasMarkdownPane: doc.MarkdownPanel,
		DocTitle:        doc.Title,
	}
	if strings.TrimSpace(data.Title) == "" {
		data.Title = "Untitled"
		data.DocTitle = ""
	}
	if err := s.templates.ExecuteTemplate(w, "document.html", data); err != nil {
		s.log.Error("template document", "err", err)
	}
}

func (s *Server) handleDocAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	docID := chi.URLParam(r, "id")
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
	if !doc.PasswordHash.Valid || doc.PasswordHash.String == "" {
		http.Redirect(w, r, docPathPrefix(docID), http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	password := r.FormValue("password")
	if !CheckPassword(doc.PasswordHash.String, password) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = s.templates.ExecuteTemplate(w, "password.html", pageData{
			Title:     "Unlock document",
			DocID:     docID,
			AuthError: true,
		})
		return
	}
	tok, err := NewSessionToken()
	if err != nil {
		s.log.Error("session token", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	expires := time.Now().Add(s.cfg.SessionTTL)
	if err := s.store.CreateSession(r.Context(), tok, docID, expires); err != nil {
		s.log.Error("session create", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	maxAge := int(s.cfg.SessionTTL.Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	setSessionCookie(w, docID, tok, maxAge)
	http.Redirect(w, r, docPathPrefix(docID), http.StatusSeeOther)
}

type settingsDTO struct {
	Title         string `json:"title"`
	MarkdownPanel bool   `json:"markdown_panel"`
	Password      string `json:"password"`
	ClearPassword bool   `json:"clear_password"`
}

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	docID := chi.URLParam(r, "id")
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
		s.log.Error("access", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	hasPW := doc.PasswordHash.Valid && doc.PasswordHash.String != ""
	_ = json.NewEncoder(w).Encode(map[string]any{
		"title":          doc.Title,
		"has_password":   hasPW,
		"markdown_panel": doc.MarkdownPanel,
	})
}

func (s *Server) handleSettingsPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	docID := chi.URLParam(r, "id")
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
		s.log.Error("access", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var body settingsDTO
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	var pwPtr *string
	switch {
	case body.ClearPassword:
		empty := ""
		pwPtr = &empty
	case strings.TrimSpace(body.Password) != "":
		h, err := HashPassword(body.Password, s.cfg.BcryptCost)
		if err != nil {
			s.log.Error("bcrypt", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		pwPtr = &h
	default:
		pwPtr = nil
	}

	if err := s.store.UpdateDocumentSettings(r.Context(), docID, body.Title, body.MarkdownPanel, pwPtr); err != nil {
		s.log.Error("update settings", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if pwPtr != nil {
		cur := SessionTokenFromRequest(r)
		if err := s.store.DeleteSessionsExcept(r.Context(), docID, cur); err != nil {
			s.log.Error("sessions", "err", err)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}
