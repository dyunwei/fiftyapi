package store

import (
	"errors"
	"github.com/gofrs/flock"
	"os"
	"path/filepath"
)

func (s *Store) LockServer() (func(), error) { return LockServerPath(s.path) }

// Acquire before opening/migrating the database, and hold until it is closed.
func LockServerPath(path string) (func(), error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
		return nil, err
	}
	lock := flock.New(absolute + ".server.lock")
	ok, err := lock.TryLock()
	if err != nil {
		lock.Close()
		return nil, err
	}
	if !ok {
		lock.Close()
		return nil, errors.New("已有服务实例正在使用此数据库；单实例模式不允许重复启动")
	}
	return func() { lock.Close() }, nil
}
