package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fiftyapi/internal/store"
	"io"
	"os"
	"path/filepath"
)

// VACUUM INTO creates a transactionally consistent standalone SQLite database,
// including committed WAL content. The source database is never copied live.
func archive(ctx context.Context, s *store.Store, dir string) (string, int64, string, error) {
	snapshot := filepath.Join(dir, "fiftyapi.db")
	if _, err := s.DB.ExecContext(ctx, "VACUUM INTO ?", snapshot); err != nil {
		return "", 0, "", errors.New("创建数据库一致性快照失败")
	}
	if err := os.Chmod(snapshot, 0600); err != nil {
		return "", 0, "", err
	}
	db, err := sql.Open("sqlite", snapshot)
	if err != nil {
		return "", 0, "", err
	}
	var check string
	err = db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&check)
	db.Close()
	if err != nil || check != "ok" {
		return "", 0, "", errors.New("数据库快照完整性检查失败")
	}
	input, err := os.Open(snapshot)
	if err != nil {
		return "", 0, "", err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return "", 0, "", err
	}
	sum := sha256.New()
	if _, err = io.Copy(sum, contextReader{ctx: ctx, reader: input}); err != nil {
		return "", 0, "", err
	}
	manifest, _ := json.Marshal(map[string]any{"format": 1, "files": map[string]string{"fiftyapi.db": hex.EncodeToString(sum.Sum(nil))}})
	if _, err = input.Seek(0, 0); err != nil {
		return "", 0, "", err
	}
	output := filepath.Join(dir, "backup.tar.gz")
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", 0, "", err
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	write := func(name string, size int64, r io.Reader) error {
		if e := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: size}); e != nil {
			return e
		}
		_, e := io.Copy(tw, contextReader{ctx: ctx, reader: r})
		return e
	}
	err = write("fiftyapi.db", info.Size(), input)
	if err == nil {
		err = write("manifest.json", int64(len(manifest)), bytes.NewReader(manifest))
	}
	if e := tw.Close(); err == nil {
		err = e
	}
	if e := gz.Close(); err == nil {
		err = e
	}
	if e := file.Close(); err == nil {
		err = e
	}
	if err != nil {
		return "", 0, "", err
	}
	data, err := os.Open(output)
	if err != nil {
		return "", 0, "", err
	}
	defer data.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, contextReader{ctx: ctx, reader: data})
	if err != nil {
		return "", 0, "", err
	}
	// S3 single PUT is limited to 5 GiB; do not silently truncate large backups.
	if size > 5*1024*1024*1024 {
		return "", 0, "", errors.New("备份超过单文件 5 GiB 限制")
	}
	return output, size, hex.EncodeToString(hash.Sum(nil)), nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
