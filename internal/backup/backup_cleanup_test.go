package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackupDatabase_RenameFailureClosesAndRemovesTemporaryFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	content := []byte("database contents must survive a failed publication")
	require.NoError(t, os.WriteFile(path, content, 0600))
	// A non-empty directory cannot be replaced by the backup file on either OS.
	require.NoError(t, os.Mkdir(path+".backup", 0700))
	marker := filepath.Join(path+".backup", "keep")
	require.NoError(t, os.WriteFile(marker, []byte("existing destination"), 0600))

	err := BackupDatabase(context.Background(), path)
	require.ErrorContains(t, err, "failed to rename backup")
	temporary, err := filepath.Glob(filepath.Join(dir, "db-*.backup.tmp"))
	require.NoError(t, err)
	assert.Empty(t, temporary, "failed publication must not leak an open temporary backup")
	actual, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, content, actual)
	existing, err := os.ReadFile(marker)
	require.NoError(t, err)
	assert.Equal(t, "existing destination", string(existing))
}
