package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"fiftyapi/internal/credentials"
	"fiftyapi/internal/store"
)

type archiveManifest struct {
	Format        int               `json:"format"`
	Files         map[string]string `json:"files"`
	Version       string            `json:"version,omitempty"`
	SchemaVersion int               `json:"schema_version,omitempty"`
}

// Restore validates and migrates a private staging copy. Existing paths are never
// replaced. The caller must stop the service and supply its original master key.
func Restore(ctx context.Context, source, output string, key []byte) error {
	if len(key) != 32 {
		return errors.New("恢复需要原主密钥（32 字节）")
	}
	output, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("恢复目标必须是不存在的新目录")
	}
	if err = os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(output), ".fiftyapi-restore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	gz, err := gzip.NewReader(contextReader{ctx: ctx, reader: input})
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	hashes := map[string]string{}
	var manifest archiveManifest
	var total int64
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		if h.Typeflag != tar.TypeReg || hashes[h.Name] != "" {
			return errors.New("备份包含重复或非普通文件")
		}
		switch h.Name {
		case "fiftyapi.db", "fiftyapi.db-wal", "fiftyapi.db-shm", "manifest.json":
		default:
			return errors.New("备份包含不支持的文件")
		}
		if h.Size < 0 || h.Size > (32<<30)-total || (h.Name == "manifest.json" && h.Size > 1<<20) {
			return errors.New("备份解压大小超过限制（32 GiB）")
		}
		total += h.Size
		f, e := os.OpenFile(filepath.Join(stage, h.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		sum := sha256.New()
		_, e = io.Copy(io.MultiWriter(f, sum), tr)
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		hashes[h.Name] = hex.EncodeToString(sum.Sum(nil))
	}
	// Drain gzip to verify its trailer/checksum, even after tar's end markers.
	if n, e := io.Copy(io.Discard, io.LimitReader(gz, (1<<20)+1)); e != nil {
		return e
	} else if n > 1<<20 {
		return errors.New("备份包含过多尾随数据")
	}
	raw, err := os.ReadFile(filepath.Join(stage, "manifest.json"))
	if err != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Format != 1 || manifest.Files["fiftyapi.db"] == "" || len(manifest.Files) != len(hashes)-1 {
		return errors.New("备份校验清单无效")
	}
	for name, sum := range manifest.Files {
		if name == "manifest.json" || hashes[name] == "" || hashes[name] != sum {
			return errors.New("备份文件校验失败")
		}
	}
	path := filepath.Join(stage, "fiftyapi.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	err = func() error {
		var check string
		if e := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&check); e != nil || check != "ok" {
			return errors.New("备份数据库完整性检查失败")
		}
		var schema int
		if e := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0) FROM schema_migrations").Scan(&schema); e != nil {
			return e
		}
		if manifest.SchemaVersion != 0 && manifest.SchemaVersion != schema {
			return errors.New("备份数据库版本与清单不一致")
		}
		var encrypted []byte
		if e := db.QueryRowContext(ctx, "SELECT value FROM system_metadata WHERE key='master_key_check'").Scan(&encrypted); e != nil {
			return errors.New("备份缺少主密钥校验记录，无法安全恢复")
		}
		plain, e := credentials.Decrypt(key, "fiftyapi:master-key-check:v1", encrypted)
		if e != nil || string(plain) != "fiftyapi-key-verification" {
			return errors.New("主密钥与备份不匹配，请提供原主密钥")
		}
		return nil
	}()
	closeErr := db.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	// Open applies supported upgrades and rejects a schema newer than this binary.
	s, err := store.Open(ctx, path)
	if err != nil {
		return fmt.Errorf("备份版本检查或升级失败：%w", err)
	}
	if err = s.Close(); err != nil {
		return err
	}
	if err = os.Remove(filepath.Join(stage, "manifest.json")); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(stage, "master.key"), []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0600); err != nil {
		return err
	}
	for _, name := range []string{"fiftyapi.db", "master.key"} {
		f, e := os.Open(filepath.Join(stage, name))
		if e != nil {
			return e
		}
		e = f.Sync()
		f.Close()
		if e != nil {
			return e
		}
	}
	if err = syncDir(stage); err != nil {
		return err
	}
	// Create exclusively after validation. Keep the new directory private and
	// remove it if publication fails; never rename over an existing directory.
	if err = os.Mkdir(output, 0700); err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			os.RemoveAll(output)
		}
	}()
	entries, err := os.ReadDir(stage)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err = os.Rename(filepath.Join(stage, entry.Name()), filepath.Join(output, entry.Name())); err != nil {
			return err
		}
	}
	if err = syncDir(output); err != nil {
		return err
	}
	if err = syncDir(filepath.Dir(output)); err != nil {
		return err
	}
	published = true
	return nil
}

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
