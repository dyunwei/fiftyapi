package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fiftyapi/internal/credentials"
	"fmt"
	"strings"
	"time"
)

type APIKey struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Prefix    string `json:"prefix"`
	Suffix    string `json:"suffix"`
	Copyable  bool   `json:"copyable"`
	Enabled   bool   `json:"enabled"`
	CreatedAt int64  `json:"created_at"`
}

func (s *Store) CreateAPIKey(ctx context.Context, user int64, name string, encryptionKey ...[]byte) (APIKey, string, error) {
	if len(name) == 0 || len(name) > 80 {
		return APIKey{}, "", errors.New("密钥名称需为 1–80 字节")
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return APIKey{}, "", err
	}
	token := "sk-" + base64.RawURLEncoding.EncodeToString(b)
	hash := sha256.Sum256([]byte(token))
	key := APIKey{Name: name, Prefix: token[:12], Suffix: token[len(token)-4:], Enabled: true, CreatedAt: time.Now().Unix()}
	var encrypted []byte
	if len(encryptionKey) > 0 && len(encryptionKey[0]) > 0 {
		var err error
		encrypted, err = credentials.Encrypt(encryptionKey[0], apiKeyBinding(user, hash[:]), []byte(token))
		if err != nil {
			return APIKey{}, "", err
		}
		key.Copyable = true
	}
	r, err := s.DB.ExecContext(ctx, "INSERT INTO api_keys(user_id,name,prefix,suffix,token_hash,token_encrypted,created_at) VALUES(?,?,?,?,?,?,?)", user, name, key.Prefix, key.Suffix, hash[:], encrypted, key.CreatedAt)
	if err != nil {
		return APIKey{}, "", err
	}
	key.ID, _ = r.LastInsertId()
	return key, token, nil
}
func (s *Store) APIKeys(ctx context.Context, user int64) ([]APIKey, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,name,prefix,suffix,length(COALESCE(token_encrypted, x''))>0,enabled,created_at FROM api_keys WHERE user_id=? AND deleted_at IS NULL ORDER BY id DESC", user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []APIKey{}
	for rows.Next() {
		var k APIKey
		if err = rows.Scan(&k.ID, &k.Name, &k.Prefix, &k.Suffix, &k.Copyable, &k.Enabled, &k.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, k)
	}
	return result, rows.Err()
}
func (s *Store) RevokeAPIKey(ctx context.Context, user, id int64) error {
	r, err := s.DB.ExecContext(ctx, "UPDATE api_keys SET enabled=0,token_encrypted=NULL WHERE id=? AND user_id=? AND deleted_at IS NULL", id, user)
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
	r, err := s.DB.ExecContext(ctx, "UPDATE api_keys SET enabled=0,name='[deleted]',prefix='',suffix='',token_encrypted=NULL,token_hash=randomblob(32),deleted_at=? WHERE id=? AND user_id=? AND deleted_at IS NULL", time.Now().Unix(), id, user)
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
	if !strings.HasPrefix(token, "sk-") || len(token) != 46 {
		return User{}, 0, ErrNotFound
	}
	hash := sha256.Sum256([]byte(token))
	var u User
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT u.id,u.username,u.password_hash,u.role,u.enabled,u.limit_5h,u.limit_7d,u.email,u.display_name,u.password_set,k.id FROM api_keys k JOIN users u ON u.id=k.user_id WHERE k.token_hash=? AND k.enabled=1 AND k.deleted_at IS NULL AND u.enabled=1 AND u.password_set=1`, hash[:]).Scan(append(userScan(&u), &id)...)
	return u, id, err
}

func apiKeyBinding(user int64, hash []byte) string {
	return fmt.Sprintf("fiftyapi:api-key:%d:%x", user, hash)
}

// RevealAPIKey returns only the owner's enabled key, verifying its stored hash.
func (s *Store) RevealAPIKey(ctx context.Context, user, id int64, encryptionKey []byte) (string, error) {
	var encrypted, hash []byte
	err := s.DB.QueryRowContext(ctx, "SELECT token_encrypted,token_hash FROM api_keys WHERE user_id=? AND id=? AND enabled=1 AND deleted_at IS NULL", user, id).Scan(&encrypted, &hash)
	if err != nil {
		return "", err
	}
	if len(encrypted) == 0 {
		return "", ErrNotFound
	}
	plain, err := credentials.Decrypt(encryptionKey, apiKeyBinding(user, hash), encrypted)
	if err != nil {
		return "", err
	}
	actual := sha256.Sum256(plain)
	if !bytes.Equal(actual[:], hash) {
		return "", errors.New("密钥校验失败")
	}
	return string(plain), nil
}
