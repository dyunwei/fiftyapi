package store

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Explicit opt-in for a disposable online-backup copy, never a live database.
// Only Store.Open runs: no account service, maintenance, decryption or network.
func TestAccountRoutingUpgradeSnapshot(t *testing.T) {
	path := os.Getenv("FIFTYAPI_TEST_UPGRADE_COPY")
	if path == "" {
		t.Skip("requires an explicitly prepared temporary upgrade.db snapshot")
	}
	if !filepath.IsAbs(path) || filepath.Base(path) != "upgrade.db" || !strings.HasPrefix(filepath.Base(filepath.Dir(path)), "fiftyapi-upgrade-") {
		t.Fatal("only an explicit disposable upgrade.db snapshot is accepted")
	}
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		t.Fatal("snapshot must be a regular file")
	}
	ctx := context.Background()
	ro, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}).String())
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	var old int
	if err = ro.QueryRowContext(ctx, "SELECT max(version) FROM schema_migrations").Scan(&old); err != nil || (old != 7 && old != 8) {
		t.Fatal("expected an untouched schema 7 or 8 snapshot")
	}
	queries := []string{"SELECT id,name,provider,credentials,state,enabled FROM upstream_accounts ORDER BY id", "SELECT account_id,model_id FROM account_models ORDER BY account_id,model_id", "SELECT id,username,password_hash,role FROM users ORDER BY id", "SELECT id,user_id,prefix,token_hash FROM api_keys ORDER BY id"}
	read := func(db *sql.DB, q string) [][]any {
		t.Helper()
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			t.Fatal("snapshot query failed")
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		out := [][]any{}
		for rows.Next() {
			v := make([]any, len(cols))
			dest := make([]any, len(cols))
			for i := range v {
				dest[i] = &v[i]
			}
			if rows.Scan(dest...) != nil {
				t.Fatal("snapshot scan failed")
			}
			for i, value := range v {
				if b, ok := value.([]byte); ok {
					v[i] = append([]byte(nil), b...)
				}
			}
			out = append(out, v)
		}
		if rows.Err() != nil {
			t.Fatal("snapshot read failed")
		}
		return out
	}
	before := make([][][]any, len(queries))
	for i, q := range queries {
		before[i] = read(ro, q)
	}
	ro.Close()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i, q := range queries {
		if !reflect.DeepEqual(before[i], read(s.DB, q)) {
			t.Fatalf("migration changed protected record category %d", i)
		}
	}
	var version, policies, health int
	if s.DB.QueryRow("SELECT max(version) FROM schema_migrations").Scan(&version) != nil || version != 17 {
		t.Fatal("schema 17 was not applied")
	}
	if s.DB.QueryRow("SELECT count(*) FROM account_policies").Scan(&policies) != nil || policies != len(before[0]) {
		t.Fatal("account policies missing")
	}
	if s.DB.QueryRow("SELECT count(*) FROM account_health").Scan(&health) != nil || health != len(before[0]) {
		t.Fatal("account health missing")
	}
	if len(read(s.DB, "PRAGMA foreign_key_check")) != 0 {
		t.Fatal("foreign keys invalid after migration")
	}
	t.Logf("schema %d -> 9 snapshot migration passed; account ciphertext, models, users and keys preserved; foreign keys valid; no account maintenance started", old)
}
