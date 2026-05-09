package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Document is a persisted notepad document.
type Document struct {
	ID            string
	Title         string
	PasswordHash  sql.NullString
	MarkdownPanel bool
	YjsState      []byte
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// CreateDocument inserts a new document with optional password hash.
func (s *Store) CreateDocument(ctx context.Context, id, title string, passwordHash *string, markdownPanel bool) error {
	now := time.Now().Unix()
	var ph any
	if passwordHash != nil {
		ph = *passwordHash
	}
	mp := 0
	if markdownPanel {
		mp = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO documents (id, title, password_hash, markdown_panel, yjs_state, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NULL, ?, ?)`,
		id, title, ph, mp, now, now,
	)
	return err
}

// GetDocument loads a document by id.
func (s *Store) GetDocument(ctx context.Context, id string) (*Document, error) {
	var d Document
	var ph sql.NullString
	var mp int
	var created, updated int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, title, password_hash, markdown_panel, yjs_state, created_at, updated_at
		 FROM documents WHERE id = ?`, id,
	).Scan(&d.ID, &d.Title, &ph, &mp, &d.YjsState, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.PasswordHash = ph
	d.MarkdownPanel = mp != 0
	d.CreatedAt = time.Unix(created, 0)
	d.UpdatedAt = time.Unix(updated, 0)
	return &d, nil
}

// UpdateDocumentSettings updates title, markdown panel, and optionally password hash (nil = leave unchanged, empty string pointer = clear).
func (s *Store) UpdateDocumentSettings(ctx context.Context, id string, title string, markdownPanel bool, passwordHash *string) error {
	mp := 0
	if markdownPanel {
		mp = 1
	}
	now := time.Now().Unix()

	if passwordHash == nil {
		_, err := s.db.ExecContext(ctx,
			`UPDATE documents SET title = ?, markdown_panel = ?, updated_at = ? WHERE id = ?`,
			title, mp, now, id,
		)
		return err
	}
	var ph any
	if *passwordHash != "" {
		ph = *passwordHash
	} else {
		ph = nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE documents SET title = ?, markdown_panel = ?, password_hash = ?, updated_at = ? WHERE id = ?`,
		title, mp, ph, now, id,
	)
	return err
}

// UpdateYjsState persists the opaque Yjs document state.
func (s *Store) UpdateYjsState(ctx context.Context, id string, state []byte) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx,
		`UPDATE documents SET yjs_state = ?, updated_at = ? WHERE id = ?`,
		state, now, id,
	)
	return err
}

// CreateSession inserts a session token for a document.
func (s *Store) CreateSession(ctx context.Context, token, documentID string, expiresAt time.Time) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO document_sessions (token, document_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		token, documentID, now, expiresAt.Unix(),
	)
	return err
}

// ValidateSession returns true if token is valid and not expired for the document.
func (s *Store) ValidateSession(ctx context.Context, token, documentID string) (bool, error) {
	var exp int64
	err := s.db.QueryRowContext(ctx,
		`SELECT expires_at FROM document_sessions WHERE token = ? AND document_id = ?`,
		token, documentID,
	).Scan(&exp)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return time.Now().Unix() < exp, nil
}

// DeleteSessionsExcept removes all sessions for a document except the given token (if non-empty).
func (s *Store) DeleteSessionsExcept(ctx context.Context, documentID, keepToken string) error {
	if keepToken == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM document_sessions WHERE document_id = ?`, documentID)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM document_sessions WHERE document_id = ? AND token != ?`,
		documentID, keepToken,
	)
	return err
}

// DeleteSession removes one session.
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM document_sessions WHERE token = ?`, token)
	return err
}
