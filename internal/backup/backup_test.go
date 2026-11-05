package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"fiftyapi/internal/store"
)

func setup(t *testing.T) (*Service, int64) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	actor, err := db.CreateAdmin(context.Background(), "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	s := New(db, make([]byte, 32))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.Close(ctx)
		db.Close()
	})
	return s, actor
}
func config(endpoint string) Config {
	c := DefaultConfig()
	c.Endpoint = endpoint
	c.Bucket = "test-bucket"
	c.AccessKey = "test-access"
	c.SecretKey = "test-secret"
	return c
}
func waitRecord(t *testing.T, s *Service, id string) Record {
	t.Helper()
	for i := 0; i < 300; i++ {
		r, err := s.Record(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if r.Status != "running" {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("backup did not finish")
	return Record{}
}

func TestBackupUploadRestoreDownloadAndOriginalTargetCleanup(t *testing.T) {
	s, actor := setup(t)
	ctx := context.Background()
	var mu sync.Mutex
	objects := map[string][]byte{}
	deletes := 0
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		// Verify the wire payload and signature from the server's escaped path.
		if hashHex(body) != r.Header.Get("X-Amz-Content-Sha256") {
			t.Error("payload hash mismatch")
		}
		canonical := r.Method + "\n" + r.URL.EscapedPath() + "\n\nhost:" + r.Host + "\nx-amz-content-sha256:" + hashHex(body) + "\nx-amz-date:" + r.Header.Get("X-Amz-Date") + "\n\nhost;x-amz-content-sha256;x-amz-date\n" + hashHex(body)
		stamp := r.Header.Get("X-Amz-Date")
		if len(stamp) != 16 {
			t.Error("missing date")
			w.WriteHeader(403)
			return
		}
		scope := stamp[:8] + "/auto/s3/aws4_request"
		hmacSHA := func(key []byte, value string) []byte {
			h := hmac.New(sha256.New, key)
			h.Write([]byte(value))
			return h.Sum(nil)
		}
		key := []byte("AWS4test-secret")
		for _, part := range []string{stamp[:8], "auto", "s3", "aws4_request"} {
			key = hmacSHA(key, part)
		}
		expected := hex.EncodeToString(hmacSHA(key, "AWS4-HMAC-SHA256\n"+stamp+"\n"+scope+"\n"+hashHex([]byte(canonical))))
		if !strings.HasSuffix(r.Header.Get("Authorization"), "Signature="+expected) || !strings.Contains(r.Header.Get("Authorization"), "Credential=test-access/") {
			t.Error("invalid signature")
			w.WriteHeader(403)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case "PUT":
			objects[r.URL.Path] = append([]byte(nil), body...)
		case "GET":
			b, ok := objects[r.URL.Path]
			if !ok {
				w.WriteHeader(404)
				return
			}
			w.Write(b)
		case "DELETE":
			delete(objects, r.URL.Path)
			deletes++
			w.WriteHeader(204)
		}
	}))
	defer upstream.Close()
	s.client = upstream.Client()
	c := config(upstream.URL)
	c.Prefix = "备份 folder/+%/"
	c.RetentionDays = 30
	if err := s.SaveConfig(ctx, actor, c); err != nil {
		t.Fatal(err)
	}
	raw, _, _ := s.store.Setting(ctx, settingName)
	if strings.Contains(raw, c.SecretKey) || strings.Contains(raw, c.AccessKey) {
		t.Fatal("credentials stored in plaintext")
	}
	if err := s.Test(ctx, c); err != nil {
		t.Fatal(err)
	}
	r, err := s.Start(actor, "manual", "", 3)
	if err != nil {
		t.Fatal(err)
	}
	r = waitRecord(t, s, r.ID)
	if r.Status != "completed" {
		t.Fatalf("backup failed: %+v", r)
	}
	if r.ExpiresAt-r.StartedAt != 3*86400 {
		t.Fatal("manual retention ignored")
	}
	if _, err := s.store.DB.Exec("INSERT INTO settings(key,value) VALUES('after_snapshot','new')"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	data := append([]byte(nil), objects["/test-bucket/"+r.ObjectKey]...)
	mu.Unlock()
	if int64(len(data)) != r.SizeBytes || hashHex(data) != r.SHA256 {
		t.Fatal("backup checksum/size mismatch")
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		files[header.Name], err = io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(files) != 2 {
		t.Fatal("unexpected archive contents")
	}
	var manifest struct {
		Format int               `json:"format"`
		Files  map[string]string `json:"files"`
	}
	if json.Unmarshal(files["manifest.json"], &manifest) != nil || manifest.Format != 1 || manifest.Files["fiftyapi.db"] != hashHex(files["fiftyapi.db"]) {
		t.Fatal("offline restore manifest invalid")
	}
	restored := filepath.Join(t.TempDir(), "restored.db")
	if err = os.WriteFile(restored, files["fiftyapi.db"], 0600); err != nil {
		t.Fatal(err)
	}
	restoredDB, err := sql.Open("sqlite", restored)
	if err != nil {
		t.Fatal(err)
	}
	defer restoredDB.Close()
	var check string
	if err = restoredDB.QueryRow("PRAGMA integrity_check").Scan(&check); err != nil || check != "ok" {
		t.Fatal("snapshot not restorable", err)
	}
	var users int
	if err = restoredDB.QueryRow("SELECT count(*) FROM users").Scan(&users); err != nil || users != 1 {
		t.Fatal("WAL data missing", err)
	}
	var after int
	if err = restoredDB.QueryRow("SELECT count(*) FROM settings WHERE key='after_snapshot'").Scan(&after); err != nil || after != 0 {
		t.Fatal("snapshot boundary violated", err)
	}
	// Changing settings must not redirect downloads/deletions to a different bucket.
	c.Bucket = "new-bucket"
	if err = s.SaveConfig(ctx, actor, c); err != nil {
		t.Fatal(err)
	}
	_, resp, err := s.Download(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	download, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(download, data) {
		t.Fatal("download changed archive")
	}
	if _, err = s.store.DB.Exec("UPDATE backup_records SET expires_at=? WHERE id=?", time.Now().Add(-time.Hour).Unix(), r.ID); err != nil {
		t.Fatal(err)
	}
	s.cleanup(ctx, time.Now())
	r, err = s.Record(ctx, r.ID)
	if err != nil || r.Status != "expired" || len(r.Target) != 0 {
		t.Fatal("expiration failed", err, r.Status)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(objects) != 0 || deletes != 2 {
		t.Fatal("cleanup did not use original target", len(objects), deletes)
	}
}

func TestFailedUploadAndConcurrentBackup(t *testing.T) {
	s, actor := setup(t)
	entered, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(403)
		w.Write([]byte("private-storage-response"))
	}))
	defer upstream.Close()
	s.client = upstream.Client()
	if err := s.SaveConfig(context.Background(), actor, config(upstream.URL)); err != nil {
		t.Fatal(err)
	}
	r, err := s.Start(actor, "manual", "")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not start")
	}
	_, busyErr := s.Start(actor, "manual", "")
	close(release)
	if busyErr != ErrBusy {
		t.Fatal("concurrent backup allowed", busyErr)
	}
	r = waitRecord(t, s, r.ID)
	if r.Status != "failed" || !strings.Contains(r.Error, "403") || strings.Contains(r.Error, "private-storage-response") {
		t.Fatalf("failure missing or unsafe: %+v", r)
	}
}

func TestHTTPEndpoint(t *testing.T) {
	for _, endpoint := range []string{"http://localhost:9000", "http://192.168.1.10:9000", "http://[::1]:9000", "https://storage.example.com"} {
		if err := config(endpoint).Validate(); err != nil {
			t.Fatalf("endpoint %q rejected: %v", endpoint, err)
		}
	}
	for _, endpoint := range []string{"ftp://localhost:9000", "http://", "http://user:password@localhost:9000", "http://localhost:9000/path", "http://localhost:9000?query=1", "http://localhost:9000#fragment"} {
		if err := config(endpoint).Validate(); err == nil {
			t.Fatalf("invalid endpoint %q accepted", endpoint)
		}
	}
	s, actor := setup(t)
	var methods []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	c := config(upstream.URL)
	ctx := context.Background()
	if err := s.SaveConfig(ctx, actor, c); err != nil {
		t.Fatal(err)
	}
	if err := s.Test(ctx, c); err != nil {
		t.Fatal(err)
	}
	if strings.Join(methods, ",") != "PUT,DELETE" {
		t.Fatalf("unexpected connection test requests: %v", methods)
	}
}

func TestSettingsRetentionAndScheduleValidation(t *testing.T) {
	s, actor := setup(t)
	ctx := context.Background()
	c := config("https://storage.example.com")
	if err := s.SaveConfig(ctx, actor, c); err != nil {
		t.Fatal(err)
	}
	c.AccessKey = ""
	c.SecretKey = ""
	c.DailyTime = "07:35"
	c.RetentionDays = 21
	if err := s.SaveConfig(ctx, actor, c); err != nil {
		t.Fatal(err)
	}
	saved, err := s.Config(ctx)
	if err != nil || saved.SecretKey != "test-secret" || saved.RetentionDays != 21 || saved.DailyTime != "07:35" {
		t.Fatal("settings did not preserve secrets/schedule", err)
	}
	for _, modify := range []func(*Config){func(c *Config) { c.Endpoint = "ftp://storage.example.com" }, func(c *Config) { c.Endpoint = "https://user:password@storage.example.com" }, func(c *Config) { c.RetentionDays = 0 }, func(c *Config) { c.DailyTime = "25:00" }, func(c *Config) { c.Prefix = "../other/" }} {
		invalid := saved
		modify(&invalid)
		if invalid.Validate() == nil {
			t.Fatal("invalid config accepted")
		}
	}
	now := time.Date(2026, 11, 3, 8, 0, 0, 0, time.FixedZone("local", 8*3600))
	saved.AutoEnabled = true
	next, err := s.NextRun(ctx, saved, now)
	if err != nil || !next.Equal(now) {
		t.Fatal("missed schedule should be due", err)
	}
	// A persisted failed automatic attempt still counts as today's one execution.
	_, err = s.store.DB.Exec(`INSERT INTO backup_records(id,status,filename,object_key,target,trigger_kind,started_at,expires_at,scheduled_day) VALUES('old','failed','old','old',X'','automatic',?,?,?)`, now.Unix(), now.Unix()+86400, now.Format("2006-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	next, err = s.NextRun(ctx, saved, now)
	if err != nil || next.Day() != 4 || next.Hour() != 7 || next.Minute() != 35 || next.Location() != now.Location() {
		t.Fatal("schedule repeated or timezone lost", err, next)
	}
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	dst := time.Date(2026, 10, 31, 12, 0, 0, 0, zone)
	tomorrow := DailyTime(saved, dst.AddDate(0, 0, 1))
	if tomorrow.Hour() != 7 || tomorrow.Minute() != 35 {
		t.Fatal("DST changed local scheduled time")
	}
}

func TestStartupRecoversInterruptedBackups(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	_, err := s.store.DB.Exec(`INSERT INTO backup_records(id,status,filename,object_key,target,trigger_kind,started_at,expires_at) VALUES('interrupted','running','old','old',X'','manual',?,?)`, time.Now().Unix(), time.Now().Add(time.Hour).Unix())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.StartBackground(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := s.Record(ctx, "interrupted")
	if err != nil || r.Status != "failed" || r.CompletedAt == nil {
		t.Fatal("interrupted backup not recovered", err)
	}
}

func TestAutomaticBackupRunsOncePerLocalDay(t *testing.T) {
	s, actor := setup(t)
	ctx := context.Background()
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body); w.WriteHeader(200) }))
	defer upstream.Close()
	s.client = upstream.Client()
	c := config(upstream.URL)
	c.AutoEnabled = true
	c.DailyTime = "00:00"
	c.RetentionDays = 9
	if err := s.SaveConfig(ctx, actor, c); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s.tick(ctx, now)
	records, total, err := s.List(ctx, 1, 20)
	if err != nil || total != 1 {
		t.Fatalf("automatic backup not started: %d %v", total, err)
	}
	r := waitRecord(t, s, records[0].ID)
	if r.Status != "completed" || r.Trigger != "automatic" || r.ActorID != 0 || r.ExpiresAt-r.StartedAt != 9*86400 {
		t.Fatalf("incorrect automatic backup: %+v", r)
	}
	s.tick(ctx, now.Add(time.Minute))
	_, total, err = s.List(ctx, 1, 20)
	if err != nil || total != 1 {
		t.Fatal("automatic backup duplicated", total, err)
	}
}
