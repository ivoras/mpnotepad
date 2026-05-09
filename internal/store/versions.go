package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
	"unicode/utf8"
)

// DocumentVersion is a flattened text snapshot of a document at a point in time.
type DocumentVersion struct {
	ID         int64
	DocumentID string
	CreatedAt  time.Time
	Text       string
	CharCount  int
	ByteSize   int
}

// DocumentVersionMeta is a lightweight listing entry (no text body).
type DocumentVersionMeta struct {
	ID         int64
	DocumentID string
	CreatedAt  time.Time
	CharCount  int
	ByteSize   int
}

// CreateDocumentVersion inserts a new flattened snapshot. Returns the new row id.
func (s *Store) CreateDocumentVersion(ctx context.Context, documentID, text string) (int64, error) {
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO document_versions (document_id, created_at, text, char_count, byte_size)
		 VALUES (?, ?, ?, ?, ?)`,
		documentID, now, text, utf8.RuneCountInString(text), len(text),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// LatestDocumentVersionText returns the text of the most recent version, or "" if none.
func (s *Store) LatestDocumentVersionText(ctx context.Context, documentID string) (string, error) {
	var text string
	err := s.db.QueryRowContext(ctx,
		`SELECT text FROM document_versions WHERE document_id = ? ORDER BY id DESC LIMIT 1`,
		documentID,
	).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return text, nil
}

// ListDocumentVersions returns versions (newest first) without their text bodies.
func (s *Store) ListDocumentVersions(ctx context.Context, documentID string, limit int) ([]DocumentVersionMeta, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, document_id, created_at, char_count, byte_size
		 FROM document_versions WHERE document_id = ? ORDER BY id DESC LIMIT ?`,
		documentID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DocumentVersionMeta
	for rows.Next() {
		var m DocumentVersionMeta
		var created int64
		if err := rows.Scan(&m.ID, &m.DocumentID, &created, &m.CharCount, &m.ByteSize); err != nil {
			return nil, err
		}
		m.CreatedAt = time.Unix(created, 0)
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetDocumentVersion fetches one version by id (scoped to a document).
func (s *Store) GetDocumentVersion(ctx context.Context, documentID string, versionID int64) (*DocumentVersion, error) {
	var v DocumentVersion
	var created int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, document_id, created_at, text, char_count, byte_size
		 FROM document_versions WHERE id = ? AND document_id = ?`,
		versionID, documentID,
	).Scan(&v.ID, &v.DocumentID, &created, &v.Text, &v.CharCount, &v.ByteSize)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v.CreatedAt = time.Unix(created, 0)
	return &v, nil
}
