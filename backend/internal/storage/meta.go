package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
)

// MetaGet reads one stored header value. It is the single-key form of an
// unexported helper so that the store owns the handle; MetaInt builds on it.
func (s *WorldStore) MetaGet(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	return value, err
}

// MetaGetTx reads a header inside a caller's transaction.
func MetaGetTx(ctx context.Context, tx *sql.Tx, key string) (string, error) {
	var value string
	err := tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	return value, err
}

// MetaSetTx writes a header inside a caller's transaction.
func MetaSetTx(ctx context.Context, tx *sql.Tx, key, value string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// MetaInt reads one stored header as an integer.
func (s *WorldStore) MetaInt(ctx context.Context, key string) (int64, error) {
	value, err := s.MetaGet(ctx, key)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid meta %s: %w", key, err)
	}
	return n, nil
}
