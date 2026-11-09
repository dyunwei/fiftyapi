package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fiftyapi/internal/store"
)

func TestRestoreArchive(t *testing.T) {
	ctx := context.Background()
	s, _ := setup(t)
	key := make([]byte, 32)
	if err := s.store.CheckMasterKey(ctx, key); err != nil {
		t.Fatal(err)
	}
	// Simulate a schema-13 backup; restore must upgrade only its copy.
	for _, column := range []string{"reasoning_effort", "thinking_type", "thinking_budget_tokens", "speed", "service_tier"} {
		if _, err := s.store.DB.Exec("ALTER TABLE request_logs DROP COLUMN " + column); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.store.DB.Exec("DROP INDEX IF EXISTS account_models_model"); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"protocols", "upstream_id"} {
		if _, err := s.store.DB.Exec("ALTER TABLE account_models DROP COLUMN " + column); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.store.DB.Exec("DROP TABLE imported_users; DELETE FROM schema_migrations WHERE version>=14"); err != nil {
		t.Fatal(err)
	}
	source, _, _, err := archive(ctx, s.store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "restored")
	if err = Restore(ctx, source, output, key); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, filepath.Join(output, "fiftyapi.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var users int
	if err = db.DB.QueryRow("SELECT count(*) FROM users").Scan(&users); err != nil || users != 1 {
		t.Fatal(users, err)
	}
	var migrated int
	if err = db.DB.QueryRow("SELECT count(*) FROM schema_migrations WHERE version=14").Scan(&migrated); err != nil || migrated != 1 {
		t.Fatal("missing schema upgrade", err)
	}
	if err = db.DB.QueryRow("SELECT count(*) FROM schema_migrations WHERE version=16").Scan(&migrated); err != nil || migrated != 1 {
		t.Fatal("missing model route schema upgrade", err)
	}
	if err = s.store.DB.QueryRow("SELECT count(*) FROM schema_migrations WHERE version=14").Scan(&migrated); err != nil || migrated != 0 {
		t.Fatal("source database changed", err)
	}
	if err = db.CheckMasterKey(ctx, key); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(output, "master.key"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("key permissions", err)
	}
	if err = Restore(ctx, source, output, key); err == nil {
		t.Fatal("overwrote existing target")
	}
	wrong := bytes.Repeat([]byte{1}, 32)
	target := filepath.Join(t.TempDir(), "wrong-key")
	if err = Restore(ctx, source, target, wrong); err == nil || !strings.Contains(err.Error(), "主密钥") {
		t.Fatal(err)
	}
	if _, err = os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("failed restore published output")
	}
}

func TestRestoreRejectsInvalidArchives(t *testing.T) {
	ctx := context.Background()
	s, _ := setup(t)
	key := make([]byte, 32)
	if err := s.store.CheckMasterKey(ctx, key); err != nil {
		t.Fatal(err)
	}
	// A valid SQLite file carrying a future schema must be rejected without publication.
	if _, err := s.store.DB.Exec("INSERT INTO schema_migrations(version,applied_at) VALUES(9999,0)"); err != nil {
		t.Fatal(err)
	}
	source, _, _, err := archive(ctx, s.store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "future")
	if err = Restore(ctx, source, output, key); err == nil || !strings.Contains(err.Error(), "版本") {
		t.Fatal(err)
	}
	if _, err = os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("published future schema")
	}
	for _, name := range []string{"checksum", "traversal", "symlink", "duplicate", "corrupt-db"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bad.tar.gz")
			f, e := os.Create(path)
			if e != nil {
				t.Fatal(e)
			}
			gz := gzip.NewWriter(f)
			tw := tar.NewWriter(gz)
			data := []byte("invalid sqlite")
			manifest, _ := json.Marshal(archiveManifest{Format: 1, Files: map[string]string{"fiftyapi.db": hashHex(data)}})
			if name == "checksum" {
				data = []byte("changed")
			}
			header := &tar.Header{Name: "fiftyapi.db", Mode: 0600, Size: int64(len(data))}
			if name == "traversal" {
				header.Name = "../fiftyapi.db"
			}
			if name == "symlink" {
				header.Typeflag = tar.TypeSymlink
				header.Linkname = "/tmp/target"
				header.Size = 0
			}
			tw.WriteHeader(header)
			if name != "symlink" {
				tw.Write(data)
			}
			if name == "duplicate" {
				tw.WriteHeader(header)
				tw.Write(data)
			}
			tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0600, Size: int64(len(manifest))})
			tw.Write(manifest)
			tw.Close()
			gz.Close()
			f.Close()
			output := filepath.Join(t.TempDir(), "output")
			if e = Restore(ctx, path, output, key); e == nil {
				t.Fatal("invalid archive accepted")
			}
			if _, e = os.Stat(output); !os.IsNotExist(e) {
				t.Fatal("published invalid restore")
			}
		})
	}
}
