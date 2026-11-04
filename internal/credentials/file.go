// Package credentials provides encrypted local storage for development probes.
// The encryption codec can also be used by the future SQLite account repository.
package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
)

const maxSize = 1 << 20

// LoadKey creates a 0600 key only if it does not exist. An existing invalid key
// is an error, never an excuse to overwrite it and lose access to credentials.
func LoadKey(path string) ([]byte, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, errors.New("无法创建本地密钥目录")
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		encoded := []byte(base64.StdEncoding.EncodeToString(b))
		_, writeErr := f.Write(encoded)
		syncErr := f.Sync()
		closeErr := f.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil {
			os.Remove(path)
			return nil, errors.New("无法保存本地密钥")
		}
		return b, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, errors.New("无法打开本地密钥")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("无法读取本地密钥")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("本地密钥权限必须限制为当前用户（chmod 600）")
	}
	key, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil || len(key) != 32 {
		return nil, errors.New("本地密钥格式错误，未覆盖已有密钥")
	}
	return key, nil
}

func aead(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("加密密钥必须为 32 字节")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Save atomically replaces an encrypted credential file. binding authenticates
// which provider/client/endpoints may reuse this credential.
func Save(path string, key []byte, binding string, plain []byte) error {
	if len(plain) > maxSize {
		return errors.New("凭据超过大小限制")
	}
	gcm, err := aead(key)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	data := append([]byte("ZTM1"), nonce...)
	data = gcm.Seal(data, nonce, plain, []byte(binding))
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return errors.New("无法创建会话目录")
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".session-*")
	if err != nil {
		return errors.New("无法创建会话临时文件")
	}
	defer os.Remove(tmp.Name())
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil || closeErr != nil {
		return errors.New("无法写入加密会话")
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return errors.New("无法替换加密会话")
	}
	return nil
}

func Load(path string, key []byte, binding string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, errors.New("无法读取会话文件")
	}
	defer f.Close()
	gcm, err := aead(key)
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, maxSize+128))
	if err != nil || len(b) > maxSize+64 || len(b) < 4+gcm.NonceSize()+gcm.Overhead() || string(b[:4]) != "ZTM1" {
		return nil, errors.New("会话文件格式错误")
	}
	nonce := b[4 : 4+gcm.NonceSize()]
	plain, err := gcm.Open(nil, nonce, b[4+gcm.NonceSize():], []byte(binding))
	if err != nil {
		return nil, errors.New("会话解密失败：密钥或供应商配置不匹配，或文件已损坏")
	}
	return plain, nil
}
