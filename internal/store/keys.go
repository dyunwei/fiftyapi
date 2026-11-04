package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"
)

type APIKey struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Prefix    string `json:"prefix"`
	Enabled   bool   `json:"enabled"`
	CreatedAt int64  `json:"created_at"`
}

func (s *Store) CreateAPIKey(ctx context.Context, user int64, name string) (APIKey, string, error) {
	if len(name) == 0 || len(name) > 80 {
		return APIKey{}, "", errors.New("密钥名称需为 1–80 字节")
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return APIKey{}, "", err
	}
	token := "fifty_" + base64.RawURLEncoding.EncodeToString(b)
	hash := sha256.Sum256([]byte(token))
	key := APIKey{Name: name, Prefix: token[:12], Enabled: true, CreatedAt: time.Now().Unix()}
	r, err := s.DB.ExecContext(ctx, "INSERT INTO api_keys(user_id,name,prefix,token_hash,created_at) VALUES(?,?,?,?,?)", user, name, key.Prefix, hash[:], key.CreatedAt)
	if err != nil {
		return APIKey{}, "", err
	}
	key.ID, _ = r.LastInsertId()
	return key, token, nil
}
func (s *Store) APIKeys(ctx context.Context, user int64) ([]APIKey, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,name,prefix,enabled,created_at FROM api_keys WHERE user_id=? AND deleted_at IS NULL ORDER BY id DESC", user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []APIKey{}
	for rows.Next() {
		var k APIKey
		if err = rows.Scan(&k.ID, &k.Name, &k.Prefix, &k.Enabled, &k.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, k)
	}
	return result, rows.Err()
}
func (s *Store) RevokeAPIKey(ctx context.Context, user, id int64) error {
	r, err := s.DB.ExecContext(ctx, "UPDATE api_keys SET enabled=0 WHERE id=? AND user_id=? AND deleted_at IS NULL", id, user)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteAPIKey removes the key from management and erases its identifying data.
// A disabled tombstone preserves historical and in-flight charge foreign keys.
func (s *Store) DeleteAPIKey(ctx context.Context, user, id int64) error {
	r, err := s.DB.ExecContext(ctx, "UPDATE api_keys SET enabled=0,name='[deleted]',prefix='',token_hash=randomblob(32),deleted_at=? WHERE id=? AND user_id=? AND deleted_at IS NULL", time.Now().Unix(), id, user)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}
func (s *Store) AuthenticateAPIKey(ctx context.Context, token string) (User, int64, error) {
	if !strings.HasPrefix(token, "fifty_") || len(token) != 49 {
		return User{}, 0, ErrNotFound
	}
	hash := sha256.Sum256([]byte(token))
	var u User
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT u.id,u.username,u.password_hash,u.role,u.enabled,u.limit_5h,u.limit_7d,u.email,u.display_name,u.password_set,k.id FROM api_keys k JOIN users u ON u.id=k.user_id WHERE k.token_hash=? AND k.enabled=1 AND k.deleted_at IS NULL AND u.enabled=1 AND u.password_set=1`, hash[:]).Scan(append(userScan(&u), &id)...)
	return u, id, err
}
