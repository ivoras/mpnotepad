package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"golang.org/x/crypto/bcrypt"

	"mpnotepad/internal/store"
)

const sessionCookieName = "mpn_sess"

func docPathPrefix(docID string) string {
	return "/d/" + docID
}

// SessionTokenFromRequest returns the session cookie value.
// Browsers only send name=value in the Cookie header (no Path), so we never
// inspect c.Path. Pair the token with document_id in ValidateSession.
func SessionTokenFromRequest(r *http.Request) string {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return ""
	}
	return c.Value
}

func setSessionCookie(w http.ResponseWriter, docID, token string, maxAgeSec int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     docPathPrefix(docID),
		MaxAge:   maxAgeSec,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, docID string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     docPathPrefix(docID),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// NewSessionToken generates a random hex token.
func NewSessionToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// CheckPassword compares plaintext with bcrypt hash.
func CheckPassword(hash, password string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// HashPassword bcrypt-hashes a password.
func HashPassword(password string, cost int) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// HasAccess returns true if the request may access the document (no password or valid session).
func HasAccess(ctx context.Context, st *store.Store, doc *store.Document, r *http.Request) (bool, error) {
	if doc == nil {
		return false, nil
	}
	if !doc.PasswordHash.Valid || doc.PasswordHash.String == "" {
		return true, nil
	}
	tok := SessionTokenFromRequest(r)
	if tok == "" {
		return false, nil
	}
	ok, err := st.ValidateSession(ctx, tok, doc.ID)
	return ok, err
}
