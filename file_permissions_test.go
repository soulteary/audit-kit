package audit

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileStorage_CreatesOwnerOnlyFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}

	dir := filepath.Join(t.TempDir(), "audit")
	path := filepath.Join(dir, "audit.log")

	storage, err := NewFileStorage(path)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	dirInfo, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())

	fileInfo, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())

	// The file opened after rotation must be owner-only as well.
	require.NoError(t, storage.Write(context.Background(), &Record{EventType: EventLoginSuccess, UserID: "u"}))
	require.NoError(t, storage.Rotate())
	fileInfo, err = os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())
}
