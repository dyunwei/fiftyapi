package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	DB   *sql.DB
	path string
}

func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("数据库路径不能为空")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
		return nil, err
	}
	// Pre-create with restrictive permissions, avoiding a world-readable DB.
	f, err := os.OpenFile(absolute, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err = os.Chmod(absolute, 0600); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", absolute)
	if err != nil {
		return nil, err
	}
	// One connection serializes short transactions; external I/O must not hold it.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	fail := func(e error) (*Store, error) { db.Close(); return nil, e }
	for _, pragma := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000", "PRAGMA synchronous=FULL"} {
		if _, err = db.ExecContext(ctx, pragma); err != nil {
			return fail(err)
		}
	}
	if err = migrate(ctx, db); err != nil {
		return fail(err)
	}
	return &Store{DB: db, path: absolute}, nil
}
func migrate(ctx context.Context, db *sql.DB) (result error) {
	// SQLite table replacement must disable FK enforcement before the transaction.
	// No store is exposed until migration and foreign_key_check both succeed.
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		return err
	}
	defer func() {
		restore, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(restore, "PRAGMA foreign_keys=ON"); err != nil && result == nil {
			result = errors.New("无法恢复数据库外键约束")
		}
	}()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)"); err != nil {
		return err
	}
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	var current int
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0) FROM schema_migrations").Scan(&current); err != nil {
		return err
	}
	if current > len(files) {
		return errors.New("数据库版本比程序新，拒绝降级写入")
	}
	for _, file := range files {
		version, err := strconv.Atoi(strings.Split(filepath.Base(file), "_")[0])
		if err != nil {
			return err
		}
		if version <= current {
			continue
		}
		script, err := migrations.ReadFile(file)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, string(script)); err != nil {
			return fmt.Errorf("数据库迁移 %d 失败：%w", version, err)
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)", version, time.Now().Unix()); err != nil {
			return err
		}
	}

	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	invalid := rows.Next()
	scanErr := rows.Err()
	rows.Close()
	if scanErr != nil {
		return scanErr
	}
	if invalid {
		return errors.New("数据库迁移检测到外键不一致，已回滚")
	}
	return tx.Commit()
}
func (s *Store) Close() error { return s.DB.Close() }
