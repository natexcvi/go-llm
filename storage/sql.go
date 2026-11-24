package storage

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
)

// SQL stores documents in an application-owned SQLite or PostgreSQL database.
// A namespace keeps tenants, conversations and memory separate in the same table.
type SQL struct {
	db    *sql.DB
	scope string
}

func NewSQL(ctx context.Context, db *sql.DB, namespace string) (*SQL, error) {
	if db == nil || namespace == "" {
		return nil, errors.New("database and namespace are required")
	}
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS go_llm_documents (
		scope TEXT NOT NULL, name TEXT NOT NULL, body BYTEA NOT NULL,
		PRIMARY KEY (scope, name))`)
	if err != nil {
		return nil, err
	}
	return &SQL{db, namespace}, nil
}
func (s *SQL) Get(ctx context.Context, key string) ([]byte, error) {
	if !valid(key) {
		return nil, fs.ErrInvalid
	}
	var b []byte
	err := s.db.QueryRowContext(ctx, "SELECT body FROM go_llm_documents WHERE scope=$1 AND name=$2", s.scope, key).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		err = fs.ErrNotExist
	}
	return b, err
}
func (s *SQL) Put(ctx context.Context, key string, b []byte) error {
	if !valid(key) {
		return fs.ErrInvalid
	}
	if b == nil {
		b = []byte{}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO go_llm_documents (scope,name,body) VALUES ($1,$2,$3)
		ON CONFLICT (scope,name) DO UPDATE SET body=excluded.body`, s.scope, key, b)
	return err
}
func (s *SQL) Delete(ctx context.Context, key string) error {
	if !valid(key) {
		return fs.ErrInvalid
	}
	res, err := s.db.ExecContext(ctx, "DELETE FROM go_llm_documents WHERE scope=$1 AND name=$2", s.scope, key)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return fs.ErrNotExist
	}
	return err
}
func (s *SQL) List(ctx context.Context) ([]string, error) {
	return s.list(ctx, "SELECT name FROM go_llm_documents WHERE scope=$1 ORDER BY name", s.scope)
}
func (s *SQL) list(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}
