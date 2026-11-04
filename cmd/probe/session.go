package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"fiftyapi/internal/credentials"
	"fiftyapi/internal/providers"
)

type savedSession struct {
	Token     providers.Token `json:"token"`
	ExpiresAt time.Time       `json:"expires_at"`
}
type sessionFile struct {
	path    string
	key     []byte
	binding string
}

func openSession(path, keyPath string, c providers.Config) (*sessionFile, *savedSession, error) {
	if path == "" && keyPath == "" {
		return nil, nil, nil
	}
	if path == "" || keyPath == "" {
		return nil, nil, errors.New("必须同时指定不同的 -session-file 和 -key-file")
	}
	var err error
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, nil, err
	}
	keyPath, err = filepath.Abs(keyPath)
	if err != nil {
		return nil, nil, err
	}
	if path == keyPath {
		return nil, nil, errors.New("会话文件与密钥文件不能为同一文件")
	}
	for _, p := range []string{path, keyPath} {
		if info, e := os.Lstat(p); e == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
			return nil, nil, errors.New("会话和密钥必须是普通文件，不能为符号链接")
		}
	}
	// Never generate a replacement key if credentials already exist.
	if _, err := os.Stat(path); err == nil {
		if _, err = os.Stat(keyPath); err != nil {
			return nil, nil, errors.New("已有会话但缺少密钥，已停止；请恢复原密钥")
		}
	}
	key, err := credentials.LoadKey(keyPath)
	if err != nil {
		return nil, nil, err
	}
	binding, _ := json.Marshal([]string{c.Provider, c.ClientID, c.AuthorizeURL, c.TokenURL, c.RedirectURI, c.Scope, c.ModelsURL})
	store := &sessionFile{path: path, key: key, binding: string(binding)}
	data, err := credentials.Load(path, key, store.binding)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var session savedSession
	if json.Unmarshal(data, &session) != nil || session.Token.AccessToken == "" || session.ExpiresAt.IsZero() {
		return nil, nil, errors.New("已保存会话缺少有效凭据或过期时间")
	}
	return store, &session, nil
}

func (s *sessionFile) save(t providers.Token, expires time.Time) error {
	if s == nil {
		return nil
	}
	data, err := json.Marshal(savedSession{Token: t, ExpiresAt: expires})
	if err != nil {
		return err
	}
	return credentials.Save(s.path, s.key, s.binding, data)
}
