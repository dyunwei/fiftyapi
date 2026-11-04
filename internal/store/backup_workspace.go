package store

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// Backups can exceed the container's small /tmp tmpfs. Stage alongside the
// database on its persistent writable volume, inside a private temporary directory.
func (s *Store) NewBackupWorkspace() (string, error) {
	return os.MkdirTemp(filepath.Dir(s.path), s.backupWorkspacePrefix())
}

// The serving process holds LockServerPath before calling this at startup.
// Only private workspaces with our generated names are eligible for cleanup.
func (s *Store) CleanupBackupWorkspaces() error {
	root := filepath.Dir(s.path)
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	generated := regexp.MustCompile("^" + regexp.QuoteMeta(s.backupWorkspacePrefix()) + "[0-9]+$")
	for _, entry := range entries {
		if !entry.IsDir() || !generated.MatchString(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().Perm() != 0700 {
			continue
		}
		if err = os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) backupWorkspacePrefix() string {
	sum := sha256.Sum256([]byte(s.path))
	return fmt.Sprintf(".fiftyapi-backup-%x-", sum[:8])
}
