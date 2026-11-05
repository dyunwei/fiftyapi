package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fiftyapi/internal/credentials"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

// Migration structs contain secrets and must never be returned or logged.
type MigrationUser struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
	Status       string `json:"status"`
	TOTPEnabled  bool   `json:"totp_enabled"`
}
type MigrationKey struct {
	ID          int64      `json:"id"`
	UserID      int64      `json:"user_id"`
	Name        string     `json:"name"`
	Key         string     `json:"key"`
	Status      string     `json:"status"`
	GroupID     *int64     `json:"group_id"`
	IPWhitelist []string   `json:"ip_whitelist"`
	IPBlacklist []string   `json:"ip_blacklist"`
	Quota       float64    `json:"quota"`
	Limit5      float64    `json:"rate_limit_5h"`
	Limit1      float64    `json:"rate_limit_1d"`
	Limit7      float64    `json:"rate_limit_7d"`
	ExpiresAt   *time.Time `json:"expires_at"`
}
type MigrationItem struct {
	Kind     string `json:"kind"`
	SourceID int64  `json:"source_id"`
	Name     string `json:"name"`
	ID       int64  `json:"id,omitempty"`
	Status   string `json:"status"`
	Enabled  bool   `json:"enabled"`
	Warning  string `json:"warning,omitempty"`
}

// ValidAPIKeyToken accepts legacy keys while bounding authentication work. Only
// tokens whose SHA-256 is already stored can authenticate; new keys stay unchanged.
func ValidAPIKeyToken(token string) bool {
	if len(token) < 16 || len(token) > 128 {
		return false
	}
	for _, c := range token {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}
func validMigrationText(s string, max int) bool {
	return s != "" && len(s) <= max && strings.IndexFunc(s, unicode.IsControl) < 0
}

// ImportSub2APIUsers preserves source ownership, never links keys to a user by
// name, and commits users and keys together. Preview executes the same checks
// in a rolled-back transaction, including duplicates inside the supplied bundle.
func (s *Store) ImportSub2APIUsers(ctx context.Context, actor int64, source string, users []MigrationUser, keys []MigrationKey, master []byte, preview, acceptKeyPolicy bool) ([]MigrationItem, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out := make([]MigrationItem, 0, len(users)+len(keys))
	mapped := map[int64]int64{}
	seenUsers := map[int64]bool{}
	now := time.Now()
	for _, u := range users {
		email := strings.ToLower(strings.TrimSpace(u.Email))
		if u.Username == "" {
			u.Username = email
		}
		item := MigrationItem{Kind: "user", SourceID: u.ID, Name: email, Status: "skipped"}
		if seenUsers[u.ID] {
			item.Warning = "迁移文件包含重复用户 ID"
			out = append(out, item)
			continue
		}
		seenUsers[u.ID] = true
		var target int64
		err = tx.QueryRowContext(ctx, "SELECT user_id FROM imported_users WHERE source=? AND source_user_id=?", source, u.ID).Scan(&target)
		if err == nil {
			var savedHash string
			if err = tx.QueryRowContext(ctx, "SELECT password_hash FROM users WHERE id=?", target).Scan(&savedHash); err != nil {
				return nil, err
			}
			mapped[u.ID] = target
			item.ID = target
			item.Warning = "已迁移，保留现有用户和预算"
			// A changed bcrypt salt can represent the same password. Report the
			// record difference without declaring either password invalid or
			// silently overwriting a password changed in FiftyAPI.
			if savedHash != u.PasswordHash {
				item.Warning = "已迁移，但密码记录与本次导出不同；保留当前密码，如无法登录请由管理员重置"
			}
			out = append(out, item)
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		address, e := mail.ParseAddress(email)
		cost, hashErr := bcrypt.Cost([]byte(u.PasswordHash))
		if u.ID <= 0 || e != nil || address.Address != email || !validMigrationText(email, 254) || len(u.PasswordHash) != 60 || hashErr != nil || cost > 16 || u.TOTPEnabled || !validMigrationText(u.Username, 320) {
			item.Warning = "用户字段无效、密码不兼容或启用了双因素认证"
			out = append(out, item)
			continue
		}
		var exists bool
		err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE username=? COLLATE NOCASE OR email=? COLLATE NOCASE)", email, email).Scan(&exists)
		if err != nil {
			return nil, err
		}
		if exists {
			item.Warning = "邮箱或账号冲突，未合并用户及其 Key"
			out = append(out, item)
			continue
		}
		enabled := u.Status == "active"
		r, e := tx.ExecContext(ctx, "INSERT INTO users(username,email,display_name,password_hash,password_set,role,enabled,limit_5h,limit_7d,created_at) VALUES(?,?,?,?,1,'user',?,?,?,?)", email, email, u.Username, u.PasswordHash, enabled, DefaultMemberBudget, DefaultMemberBudget, now.Unix())
		if e != nil {
			return nil, e
		}
		target, e = r.LastInsertId()
		if e != nil {
			return nil, e
		}
		var savedHash string
		if e = tx.QueryRowContext(ctx, "SELECT password_hash FROM users WHERE id=?", target).Scan(&savedHash); e != nil {
			return nil, e
		}
		if savedHash != u.PasswordHash {
			return nil, errors.New("迁入密码记录写入校验失败")
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO imported_users(source,source_user_id,user_id) VALUES(?,?,?)", source, u.ID, target); e != nil {
			return nil, e
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO admin_audit_logs(actor_id,action,target_id,created_at,details) VALUES(?,'import_sub2api_user',?,?,?)", actor, target, now.Unix(), "password_hash_preserved=true"); e != nil {
			return nil, e
		}
		mapped[u.ID] = target
		item.ID = target
		item.Status = "imported"
		item.Enabled = enabled
		out = append(out, item)
	}
	seenKeys := map[int64]bool{}
	for _, k := range keys {
		item := MigrationItem{Kind: "key", SourceID: k.ID, Name: k.Name, Status: "skipped"}
		owner, ok := mapped[k.UserID]
		if !ok {
			item.Warning = "所属用户未迁移，未关联到其他用户"
			out = append(out, item)
			continue
		}
		if k.ID <= 0 || seenKeys[k.ID] || !ValidAPIKeyToken(k.Key) || !validMigrationText(k.Name, 100) {
			item.Warning = "Key 字段无效或 ID 重复"
			out = append(out, item)
			continue
		}
		seenKeys[k.ID] = true
		hash := sha256.Sum256([]byte(k.Key))
		var exists bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM api_keys WHERE token_hash=?)", hash[:]).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			item.Warning = "Key 已存在，保留原所属用户和状态"
			out = append(out, item)
			continue
		}
		restricted := k.GroupID != nil || len(k.IPWhitelist) > 0 || len(k.IPBlacklist) > 0 || k.Quota != 0 || k.Limit5 != 0 || k.Limit1 != 0 || k.Limit7 != 0 || k.ExpiresAt != nil
		enabled := k.Status == "active" && (!restricted || acceptKeyPolicy)
		if k.ExpiresAt != nil && !k.ExpiresAt.After(now) {
			enabled = false
		}
		if restricted {
			if acceptKeyPolicy {
				item.Warning = "已改用 fiftyapi 全局模型权限及成员预算；不保留旧 Key 限制"
			} else {
				item.Warning = "旧 Key 含分组、额度、IP 或有效期限制，已停用"
			}
		}
		encrypted, e := credentials.Encrypt(master, apiKeyBinding(owner, hash[:]), []byte(k.Key))
		if e != nil {
			return nil, e
		}
		r, e := tx.ExecContext(ctx, "INSERT INTO api_keys(user_id,name,prefix,suffix,token_hash,token_encrypted,enabled,created_at) VALUES(?,?,?,?,?,?,?,?)", owner, k.Name, k.Key[:12], k.Key[len(k.Key)-4:], hash[:], encrypted, enabled, now.Unix())
		if e != nil {
			return nil, e
		}
		item.ID, e = r.LastInsertId()
		if e != nil {
			return nil, e
		}
		item.Status = "imported"
		item.Enabled = enabled
		if e = audit(ctx, tx, actor, "import_sub2api_key", item.ID); e != nil {
			return nil, e
		}
		out = append(out, item)
	}
	if preview {
		return out, nil
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit migration: %w", err)
	}
	return out, nil
}
