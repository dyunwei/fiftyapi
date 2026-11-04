package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"fiftyapi/internal/configenv"
)

// Called under the database instance lock when creating credentials. Never
// generate a replacement key for an existing database whose key was lost.
func masterKey(dbPath string, create bool) ([]byte, error) {
	value := configenv.Value("FIFTYAPI_MASTER_KEY")
	if value == "" {
		path := filepath.Join(filepath.Dir(dbPath), "master.key")
		saved, err := readSecret(path)
		if errors.Is(err, os.ErrNotExist) && create {
			if _, statErr := os.Stat(dbPath); !errors.Is(statErr, os.ErrNotExist) {
				return nil, errors.New("Existing database found without its master key. Restore FIFTYAPI_MASTER_KEY or the original master.key; no new key was generated.")
			}
			data := make([]byte, 32)
			if _, err = rand.Read(data); err != nil {
				return nil, err
			}
			saved = base64.StdEncoding.EncodeToString(data)
			if err = writeSecret(path, saved); err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, fmt.Errorf("Cannot read master key file %s: %w", path, err)
		}
		value = saved
	}
	key, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(key) != 32 {
		return nil, errors.New("FIFTYAPI_MASTER_KEY / master.key must contain a Base64-encoded 32-byte random key")
	}
	return key, nil
}

func initializationToken(dbPath string) (string, error) {
	value := configenv.Value("FIFTYAPI_SETUP_TOKEN")
	if value == "" {
		path := filepath.Join(filepath.Dir(dbPath), "setup-token")
		saved, err := readSecret(path)
		if errors.Is(err, os.ErrNotExist) {
			data := make([]byte, 4)
			if _, err = rand.Read(data); err != nil {
				return "", err
			}
			saved = hex.EncodeToString(data)
			if err = writeSecret(path, saved); err != nil {
				return "", err
			}
		} else if err != nil {
			return "", fmt.Errorf("Cannot read setup token file %s: %w", path, err)
		}
		value = saved
	}
	if len(value) < 8 {
		return "", errors.New("FIFTYAPI_SETUP_TOKEN / setup-token must contain at least 8 bytes")
	}
	return value, nil
}

func readSecret(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		return "", fmt.Errorf("Secret file %s must be a regular file of at most 4096 bytes with owner-only permissions (chmod 600)", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func writeSecret(path, value string) error {
	// O_EXCL avoids overwriting existing credentials, including symlinks.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = file.WriteString(value + "\n")
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}
