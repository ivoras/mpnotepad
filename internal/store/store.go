package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens SQLite at path, ensures parent directory exists, applies schema and WAL pragmas.
func Open(path string) (*Store, error) {
	path = filepath.Clean(path)
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("mkdir db dir: %w", err)
		}
	}

	dsn := "file:" + path + "?_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}

	for _, q := range []string{
		`PRAGMA journal_mode=WAL;`,
		`PRAGMA synchronous=NORMAL;`,
		`PRAGMA foreign_keys=ON;`,
		`PRAGMA busy_timeout=5000;`,
	} {
		if _, err := db.Exec(q); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("%s: %w", q, err)
		}
	}

	for _, stmt := range strings.Split(schemaSQL, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("schema: %w", err)
		}
	}

	return &Store{db: db}, nil
}

// DB returns the underlying *sql.DB for tests.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// WithTx runs fn inside a transaction.
func (s *Store) WithTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
