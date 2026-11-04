package store

import (
	"bytes"
	"context"
	"database/sql"
	"fiftyapi/internal/modelref"
	"io/fs"
	"path/filepath"
	"testing"
	"time"
)

func TestGatewayAccountMigrationPreservesParentReferences(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	files, err := fs.Glob(migrations, "migrations/00[1234]_*.sql")
	if err != nil || len(files) != 4 {
		t.Fatal("missing prior schema")
	}
	for index, file := range files {
		raw, _ := migrations.ReadFile(file)
		if _, err = db.Exec(string(raw)); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)", index+1, time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		"INSERT INTO users(id,username,password_hash,role,created_at) VALUES(1,'admin','hash','admin',1)",
		"INSERT INTO api_keys(id,user_id,name,prefix,token_hash,created_at) VALUES(1,1,'key','test',X'01',1)",
		"INSERT INTO upstream_accounts(id,name,provider,credentials,state,created_at,updated_at) VALUES(9,'legacy','antigravity',X'010203','catalog_ready',1,1)",
		"INSERT INTO models(id,provider,upstream_id,synced_at) VALUES('model','antigravity','model',1)",
		"INSERT INTO account_models(account_id,model_id) VALUES(9,'model')",
		"INSERT INTO model_prices(model_id,input,output,cached,cache_write,created_at) VALUES('model',1,1,0,0,1)",
		"INSERT INTO request_logs(id,user_id,key_id,account_id,model,provider,status,duration_ms,created_at) VALUES('old',1,1,9,'model','antigravity',200,1,1)",
		"INSERT INTO gateway_sessions(session_hash,user_id,account_id,expires_at) VALUES(X'01',1,9,9999999999)",
	} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	row, err := s.Account(ctx, 9)
	if err != nil || !bytes.Equal(row.Credentials, []byte{1, 2, 3}) || row.Provider != "antigravity" {
		t.Fatal("parent ID/credential changed")
	}
	if _, ids, err := s.RoutableModel(ctx, "model"); err != nil || len(ids) != 1 || ids[0] != 9 {
		t.Fatal("model relation lost")
	}
	if id, err := s.GatewaySession(ctx, []byte{1}); err != nil || id != 9 {
		t.Fatal("session relation lost")
	}
	logs, err := s.Logs(ctx, 1, true, 0, "", 0)
	if err != nil || len(logs) != 1 || *logs[0].AccountID != 9 {
		t.Fatal("log relation lost")
	}
	var enabled int
	if err = s.DB.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != 1 {
		t.Fatal("foreign keys not restored")
	}
	if _, err = s.DB.Exec("INSERT INTO account_models(account_id,model_id) VALUES(999,'model')"); err == nil {
		t.Fatal("foreign key enforcement disabled")
	}
	id, err := s.CreateAccount(ctx, 1, "gateway", "gateway", func(int64) ([]byte, error) { return []byte{4}, nil }, "new-api")
	if err != nil {
		t.Fatal("new gateway provider rejected")
	}
	ids, err := s.SyncGatewayModels(ctx, id, []modelref.Mapping{{ID: "alias", UpstreamID: "target", Protocol: "messages"}})
	if err != nil || len(ids) != 1 {
		t.Fatal(err)
	}
	if err = s.SetModel(ctx, 1, "alias", true, &Price{Input: 1000, Output: 2000}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SyncGatewayModels(ctx, id, []modelref.Mapping{{ID: "renamed", UpstreamID: "target", Protocol: "messages"}}); err != nil {
		t.Fatal("alias rename failed")
	}
	if price, err := s.Price(ctx, "renamed"); err != nil || price.Input != 1000 {
		t.Fatal("rename lost local pricing")
	}
	if _, err = s.SyncGatewayModels(ctx, id, []modelref.Mapping{{ID: "model", UpstreamID: "target", Protocol: "messages"}}); err == nil {
		t.Fatal("alias collision allowed")
	}
	if _, candidates, err := s.RoutableModel(ctx, "model"); err != nil || candidates[0] != 9 {
		t.Fatal("collision changed original source")
	}
}
