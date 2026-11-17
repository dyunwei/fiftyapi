package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"fiftyapi/internal/providers"
	"io"
	"net/http"
	"strings"
	"time"
)

const CodexReleaseURL = "https://api.github.com/repos/openai/codex/releases/latest"

type VersionCandidate struct {
	Version     string `json:"version"`
	URL         string `json:"url"`
	PublishedAt string `json:"published_at"`
}

func (s *Service) CheckCodex(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", CodexReleaseURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := s.Client.Do(req)
	if err != nil {
		return errors.New("无法检查 Codex 新版本")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("Codex 版本检查失败")
	}
	var release struct {
		Tag        string `json:"tag_name"`
		URL        string `json:"html_url"`
		Published  string `json:"published_at"`
		Prerelease bool   `json:"prerelease"`
		Draft      bool   `json:"draft"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release) != nil {
		return errors.New("Codex 版本信息无效")
	}
	version := strings.TrimPrefix(strings.TrimPrefix(release.Tag, "rust-"), "v")
	if release.Prerelease || release.Draft || !providers.ValidClientVersion(version) {
		return errors.New("Codex 稳定版本信息无效")
	}
	raw, _ := json.Marshal(VersionCandidate{Version: version, URL: release.URL, PublishedAt: release.Published})
	value := string(raw)
	if err = saveCandidate(ctx, s, value); err != nil {
		return err
	}
	_, err = s.Store.DB.ExecContext(ctx, `INSERT INTO maintenance_state(name,last_success,last_attempt,error,revision) VALUES('codex_version',?,?,'',?) ON CONFLICT(name) DO UPDATE SET last_success=excluded.last_success,last_attempt=excluded.last_attempt,error='',revision=excluded.revision`, time.Now().Unix(), time.Now().Unix(), version)
	return err
}
func (s *Service) MaintainVersions(ctx context.Context) {
	state, err := s.Store.MaintenanceState(ctx, "codex_version")
	if err != nil {
		return
	}
	now := time.Now().Unix()
	if now-state.LastSuccess < 86400 || now-state.LastAttempt < 3600 {
		return
	}
	err = s.CheckCodex(ctx)
	note := ""
	if err != nil {
		note = err.Error()
	}
	_ = s.Store.RecordMaintenance(ctx, "codex_version", note)
}

func saveCandidate(ctx context.Context, s *Service, value string) error {
	_, err := s.Store.DB.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES('codex_candidate',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", value)
	return err
}
