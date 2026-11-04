package backup

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fiftyapi/internal/credentials"
	"fiftyapi/internal/store"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

var ErrBusy = errors.New("已有备份正在执行，请稍后重试")
var ErrNotFound = errors.New("备份记录不存在")

type Service struct {
	store                    *store.Store
	key                      []byte
	client                   *http.Client
	mu                       sync.Mutex
	operations               sync.Mutex
	active, closing, started bool
	ctx                      context.Context
	cancel                   context.CancelFunc
	wg                       sync.WaitGroup
}
type Record struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Filename    string `json:"filename"`
	ObjectKey   string `json:"object_key"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
	Trigger     string `json:"trigger"`
	ActorID     int64  `json:"actor_id"`
	StartedAt   int64  `json:"started_at"`
	CompletedAt *int64 `json:"completed_at"`
	ExpiresAt   int64  `json:"expires_at"`
	Error       string `json:"error"`
	Target      []byte `json:"-"`
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("random source unavailable")
	}
	return hex.EncodeToString(b[:])
}
func New(s *store.Store, key []byte) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{store: s, key: append([]byte(nil), key...), ctx: ctx, cancel: cancel, client: &http.Client{Timeout: 15 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (s *Service) event(ctx context.Context, level, event, message string, duration int64) {
	s.store.RecordRuntime(ctx, store.RuntimeLog{Level: level, Module: "backup", Event: event, Message: message, DurationMS: duration})
}
func (s *Service) sealTarget(id string, c Config) ([]byte, error) {
	raw, _ := json.Marshal(c)
	defer clear(raw)
	return credentials.Encrypt(s.key, "fiftyapi:backup:"+id, raw)
}
func (s *Service) target(r Record) (Config, error) {
	raw, err := credentials.Decrypt(s.key, "fiftyapi:backup:"+r.ID, r.Target)
	if err != nil {
		return Config{}, errors.New("无法解密原备份存储配置")
	}
	defer clear(raw)
	var c Config
	err = json.Unmarshal(raw, &c)
	return c, err
}
func (s *Service) Start(actor int64, kind, day string, retention ...int) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return Record{}, errors.New("服务正在关闭")
	}
	if s.active {
		return Record{}, ErrBusy
	}
	c, err := s.Config(s.ctx)
	if err != nil {
		return Record{}, err
	}
	if err = c.Validate(); err != nil {
		return Record{}, err
	}
	if len(retention) > 0 {
		if retention[0] < 1 || retention[0] > 3650 {
			return Record{}, errors.New("保留天数需为 1–3650")
		}
		c.RetentionDays = retention[0]
	}
	now := time.Now()
	if kind == "automatic" && (!c.AutoEnabled || now.Before(DailyTime(c, now)) || day != now.Format("2006-01-02")) {
		return Record{}, errors.New("自动备份配置已改变")
	}
	id := newID()
	filename := "fiftyapi-" + now.UTC().Format("20060102T150405Z") + "-" + id + ".tar.gz"
	r := Record{ID: id, Status: "running", Filename: filename, ObjectKey: c.Prefix + filename, Trigger: kind, ActorID: actor, StartedAt: now.Unix(), ExpiresAt: now.Add(time.Duration(c.RetentionDays) * 24 * time.Hour).Unix()}
	r.Target, err = s.sealTarget(id, c)
	if err != nil {
		return r, errors.New("无法加密备份存储配置")
	}
	var scheduled any
	if day != "" {
		scheduled = day
	}
	_, err = s.store.DB.ExecContext(s.ctx, `INSERT INTO backup_records(id,status,filename,object_key,target,trigger_kind,actor_id,started_at,expires_at,scheduled_day) VALUES(?,?,?,?,?,?,?,?,?,?)`, r.ID, r.Status, r.Filename, r.ObjectKey, r.Target, kind, actor, r.StartedAt, r.ExpiresAt, scheduled)
	if err != nil {
		return Record{}, errors.New("创建备份记录失败或今日自动备份已执行")
	}
	s.active = true
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() { s.mu.Lock(); s.active = false; s.mu.Unlock() }()
		s.execute(r, c)
	}()
	return r, nil
}
func (s *Service) execute(r Record, c Config) {
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Minute)
	defer cancel()
	start := time.Now()
	s.event(ctx, "info", "started", "备份任务开始（"+r.ID+"）", 0)
	var size int64
	var checksum string
	run := func() error {
		dir, err := s.store.NewBackupWorkspace()
		if err != nil {
			return errors.New("创建备份临时目录失败")
		}
		defer os.RemoveAll(dir)
		path, n, hash, err := archive(ctx, s.store, dir)
		if err != nil {
			return err
		}
		size = n
		checksum = hash
		file, err := os.Open(path)
		if err != nil {
			return errors.New("读取备份文件失败")
		}
		defer file.Close()
		resp, err := s.object(ctx, c, http.MethodPut, r.ObjectKey, file, size, checksum)
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	}
	err := run()
	status, message := "completed", ""
	level, summary := "info", "备份任务完成（"+r.ID+"）"
	if err != nil {
		status = "failed"
		level = "error"
		message = err.Error()
		summary = "备份任务失败（" + r.ID + "）：" + message
	}
	// Persist completion even when shutdown canceled the upload.
	save, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	_, saveErr := s.store.DB.ExecContext(save, `UPDATE backup_records SET status=?,size_bytes=?,sha256=?,completed_at=?,error=? WHERE id=?`, status, size, checksum, time.Now().Unix(), message, r.ID)
	if saveErr != nil {
		level = "error"
		summary = "备份任务结果保存失败（" + r.ID + "），请检查数据库"
	}
	s.event(save, level, "finished", summary, time.Since(start).Milliseconds())
}

const recordColumns = `id,status,filename,object_key,target,size_bytes,sha256,trigger_kind,actor_id,started_at,completed_at,expires_at,error`

func scanRecord(row interface{ Scan(...any) error }) (Record, error) {
	var r Record
	err := row.Scan(&r.ID, &r.Status, &r.Filename, &r.ObjectKey, &r.Target, &r.SizeBytes, &r.SHA256, &r.Trigger, &r.ActorID, &r.StartedAt, &r.CompletedAt, &r.ExpiresAt, &r.Error)
	return r, err
}
func (s *Service) Record(ctx context.Context, id string) (Record, error) {
	r, err := scanRecord(s.store.DB.QueryRowContext(ctx, "SELECT "+recordColumns+" FROM backup_records WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return r, err
}
func (s *Service) List(ctx context.Context, page, size int) ([]Record, int, error) {
	if page < 1 || page > 1000000 || size < 1 || size > 100 {
		return nil, 0, errors.New("分页参数无效")
	}
	var total int
	if err := s.store.DB.QueryRowContext(ctx, "SELECT count(*) FROM backup_records").Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.store.DB.QueryContext(ctx, "SELECT "+recordColumns+" FROM backup_records ORDER BY started_at DESC,id DESC LIMIT ? OFFSET ?", size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []Record{}
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, r)
	}
	return items, total, rows.Err()
}
func (s *Service) Delete(ctx context.Context, id string, expired bool) error {
	s.operations.Lock()
	defer s.operations.Unlock()
	r, err := s.Record(ctx, id)
	if err != nil {
		return err
	}
	if r.Status == "running" {
		return ErrBusy
	}
	if r.Status == "deleted" || r.Status == "expired" {
		return nil
	}
	c, err := s.target(r)
	if err != nil {
		return err
	}
	if err = s.deleteObject(ctx, c, r.ObjectKey); err != nil {
		return err
	}
	status := "deleted"
	if expired {
		status = "expired"
	}
	_, err = s.store.DB.ExecContext(ctx, "UPDATE backup_records SET status=?,target=X'',error='' WHERE id=?", status, id)
	if err == nil {
		s.event(ctx, "info", "deleted", fmt.Sprintf("备份 %s 已%s", id, map[bool]string{true: "过期清理", false: "删除"}[expired]), 0)
	}
	return err
}
func (s *Service) Download(ctx context.Context, id string) (Record, *http.Response, error) {
	r, err := s.Record(ctx, id)
	if err != nil {
		return r, nil, err
	}
	if r.Status != "completed" || r.ExpiresAt <= time.Now().Unix() {
		return r, nil, errors.New("备份未完成、已删除或已过期")
	}
	c, err := s.target(r)
	if err != nil {
		return r, nil, err
	}
	resp, err := s.object(ctx, c, http.MethodGet, r.ObjectKey, nil, 0, hashHex(nil))
	return r, resp, err
}
func (s *Service) Close(ctx context.Context) error {
	// Cancel snapshots before taking the settings lock: a settings save may be
	// waiting for the single database connection currently used by VACUUM.
	s.cancel()
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
