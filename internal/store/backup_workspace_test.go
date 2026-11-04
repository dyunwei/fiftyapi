package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupWorkspaceCleanupIsScopedToDatabase(t *testing.T) {
	root := t.TempDir()
	first, err := Open(context.Background(), filepath.Join(root, "first.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(context.Background(), filepath.Join(root, "second.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	stale, err := first.NewBackupWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	other, err := second.NewBackupWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(stale); err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("workspace is not private", err)
	}
	if err = os.WriteFile(filepath.Join(stale, "snapshot.db"), []byte("temporary"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = first.CleanupBackupWorkspaces(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale workspace remains")
	}
	if _, err = os.Stat(other); err != nil {
		t.Fatal("another database's workspace was removed")
	}
}
