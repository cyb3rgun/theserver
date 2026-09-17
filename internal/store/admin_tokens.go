package store

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrAdminTokenUnknown is a bearer token that no admin token matches.
	ErrAdminTokenUnknown = errors.New("unknown admin token")
	// ErrAdminTokenRevoked is an admin token that was revoked; the API
	// answers it with 403.
	ErrAdminTokenRevoked = errors.New("admin token revoked")
	// ErrAdminTokenNotFound is an admin token id the store does not hold.
	ErrAdminTokenNotFound = errors.New("admin token not found")
)

// lastUsedEvery limits how often a token's last_used is written.
const lastUsedEvery = time.Minute

// An AdminToken is one row of admin_tokens (D-025). The token itself is never
// stored; LastUsed and RevokedAt are unix milliseconds, zero while unset.
type AdminToken struct {
	ID        string
	Name      string
	CreatedAt int64
	LastUsed  int64
	RevokedAt int64
}

// Revoked reports whether the token was revoked.
func (t AdminToken) Revoked() bool {
	return t.RevokedAt != 0
}

// AddAdminToken creates an admin token and returns its row and the token,
// which is shown to the operator once and never stored.
func (s *Store) AddAdminToken(ctx context.Context, name string) (AdminToken, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return AdminToken{}, "", errors.New("admin token name must not be empty")
	}
	token, err := NewDeviceToken()
	if err != nil {
		return AdminToken{}, "", err
	}
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return AdminToken{}, "", err
	}
	row := AdminToken{ID: "adm-" + hex.EncodeToString(raw[:]), Name: name, CreatedAt: s.nowMilli()}

	defer s.writing()()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO admin_tokens (id, name, token_hash, created_at) VALUES (?, ?, ?, ?)`,
		row.ID, row.Name, HashToken(token), row.CreatedAt)
	if err != nil {
		return AdminToken{}, "", fmt.Errorf("add admin token %s: %w", name, err)
	}
	return row, token, nil
}

// VerifyAdminToken finds the admin token a bearer token belongs to. An
// unknown token gives ErrAdminTokenUnknown, a revoked one ErrAdminTokenRevoked
// together with its row. The stored hash is compared in constant time, and
// last_used is written at most once a minute.
func (s *Store) VerifyAdminToken(ctx context.Context, token string) (AdminToken, error) {
	if token == "" {
		return AdminToken{}, ErrAdminTokenUnknown
	}
	hash := HashToken(token)
	var (
		row                 AdminToken
		stored              []byte
		lastUsed, revokedAt sql.NullInt64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, token_hash, created_at, last_used, revoked_at FROM admin_tokens WHERE token_hash = ?`, hash,
	).Scan(&row.ID, &row.Name, &stored, &row.CreatedAt, &lastUsed, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminToken{}, ErrAdminTokenUnknown
	}
	if err != nil {
		return AdminToken{}, fmt.Errorf("verify admin token: %w", err)
	}
	if subtle.ConstantTimeCompare(stored, hash) != 1 {
		return AdminToken{}, ErrAdminTokenUnknown
	}
	row.LastUsed, row.RevokedAt = lastUsed.Int64, revokedAt.Int64
	if row.Revoked() {
		return row, ErrAdminTokenRevoked
	}

	now := s.nowMilli()
	if now-row.LastUsed >= lastUsedEvery.Milliseconds() {
		if err := s.touchAdminToken(ctx, row.ID, now); err != nil {
			return AdminToken{}, err
		}
		row.LastUsed = now
	}
	return row, nil
}

func (s *Store) touchAdminToken(ctx context.Context, id string, now int64) error {
	defer s.writing()()
	if _, err := s.db.ExecContext(ctx, `UPDATE admin_tokens SET last_used = ? WHERE id = ?`, now, id); err != nil {
		return fmt.Errorf("record use of admin token %s: %w", id, err)
	}
	return nil
}

// GetAdminToken reads one admin token by id, revoked or not.
func (s *Store) GetAdminToken(ctx context.Context, id string) (AdminToken, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, created_at, last_used, revoked_at FROM admin_tokens WHERE id = ?`, id)
	token, err := scanAdminToken(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminToken{}, fmt.Errorf("%s: %w", id, ErrAdminTokenNotFound)
	}
	if err != nil {
		return AdminToken{}, fmt.Errorf("get admin token %s: %w", id, err)
	}
	return token, nil
}

// ListAdminTokens returns every admin token, revoked ones included, oldest
// first.
func (s *Store) ListAdminTokens(ctx context.Context) ([]AdminToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, created_at, last_used, revoked_at FROM admin_tokens ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list admin tokens: %w", err)
	}
	defer rows.Close()
	var tokens []AdminToken
	for rows.Next() {
		token, err := scanAdminToken(rows)
		if err != nil {
			return nil, fmt.Errorf("list admin tokens: %w", err)
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

// RevokeAdminToken marks an admin token as revoked. The row stays; revoking it
// again keeps the first time.
func (s *Store) RevokeAdminToken(ctx context.Context, id string) error {
	defer s.writing()()
	result, err := s.db.ExecContext(ctx,
		`UPDATE admin_tokens SET revoked_at = COALESCE(revoked_at, ?) WHERE id = ?`, s.nowMilli(), id)
	if err != nil {
		return fmt.Errorf("revoke admin token %s: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("%s: %w", id, ErrAdminTokenNotFound)
	}
	return nil
}

func scanAdminToken(row rowScanner) (AdminToken, error) {
	var (
		token               AdminToken
		lastUsed, revokedAt sql.NullInt64
	)
	if err := row.Scan(&token.ID, &token.Name, &token.CreatedAt, &lastUsed, &revokedAt); err != nil {
		return AdminToken{}, err
	}
	token.LastUsed, token.RevokedAt = lastUsed.Int64, revokedAt.Int64
	return token, nil
}
