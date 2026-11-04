package store

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"

	"fiftyapi/internal/credentials"
)

func (s *Store) CheckMasterKey(ctx context.Context, key []byte) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var encrypted []byte
	err = tx.QueryRowContext(ctx, "SELECT value FROM system_metadata WHERE key='master_key_check'").Scan(&encrypted)
	// Persistent cryptographic identifiers predate the FiftyAPI rename. Changing
	// either value would make existing master keys fail verification.
	const binding = "ztokenmgr:master-key-check:v1"
	const value = "ztokenmgr-key-verification"
	if errors.Is(err, sql.ErrNoRows) {
		encrypted, err = credentials.Encrypt(key, binding, []byte(value))
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO system_metadata(key,value) VALUES('master_key_check',?)", encrypted); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		plain, err := credentials.Decrypt(key, binding, encrypted)
		if err != nil || subtle.ConstantTimeCompare(plain, []byte(value)) != 1 {
			return errors.New("主密钥与已有数据库不匹配，已停止；请恢复原主密钥")
		}
	}
	return tx.Commit()
}
