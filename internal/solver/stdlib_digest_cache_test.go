package solver

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// writeDigestFile writes contents to path and, when at is non-zero, sets the file's
// modification time to at.
func writeDigestFile(t *testing.T, path, contents string, at time.Time) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
	if !at.IsZero() {
		require.NoError(t, os.Chtimes(path, at, at))
	}
}

// requireDigest asserts that the cache returns the SHA-256 of want for path.
func requireDigest(t *testing.T, cache *fileDigestCache, path, want string) {
	t.Helper()
	got, err := cache.digest(path)
	require.NoError(t, err)
	require.Equal(t, sha256.Sum256([]byte(want)), got)
}

// TestFileDigestCacheReusesAnUnchangedStamp asserts that digest returns the recorded
// digest when the size and modification time match. The second write keeps the size
// and sets the old time back, so the stamp cannot tell it from an unchanged file, and
// the first contents' digest comes back for the second contents.
func TestFileDigestCacheReusesAnUnchangedStamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.esc")
	past := time.Now().Add(-time.Hour)
	cache := newFileDigestCache()

	writeDigestFile(t, path, "abc", past)
	requireDigest(t, cache, path, "abc")
	writeDigestFile(t, path, "xyz", past)
	requireDigest(t, cache, path, "abc")
}

// TestFileDigestCacheRereadsAChangedSize asserts that digest reads the file again when
// its size changes, even with the modification time unchanged.
func TestFileDigestCacheRereadsAChangedSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.esc")
	past := time.Now().Add(-time.Hour)
	cache := newFileDigestCache()

	writeDigestFile(t, path, "abc", past)
	requireDigest(t, cache, path, "abc")
	writeDigestFile(t, path, "abcd", past)
	requireDigest(t, cache, path, "abcd")
}

// TestFileDigestCacheRereadsAChangedModTime asserts that digest reads the file again
// when its modification time changes, even with the size unchanged.
func TestFileDigestCacheRereadsAChangedModTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.esc")
	past := time.Now().Add(-time.Hour)
	cache := newFileDigestCache()

	writeDigestFile(t, path, "abc", past)
	requireDigest(t, cache, path, "abc")
	writeDigestFile(t, path, "xyz", past.Add(time.Minute))
	requireDigest(t, cache, path, "xyz")
}

// TestFileDigestCacheSkipsARecentWrite asserts that digest does not record a file
// modified within mtimeGranularity of the read. The second write keeps the size and
// sets the first write's time back, so only the missing entry makes digest read the
// file again.
func TestFileDigestCacheSkipsARecentWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.esc")
	cache := newFileDigestCache()

	writeDigestFile(t, path, "abc", time.Time{})
	requireDigest(t, cache, path, "abc")
	info, err := os.Stat(path)
	require.NoError(t, err)
	writeDigestFile(t, path, "xyz", info.ModTime())
	requireDigest(t, cache, path, "xyz")
}

// TestFileDigestCacheReportsAMissingFile asserts that digest returns the error os.Stat
// gives for a file that does not exist, and records nothing for it.
func TestFileDigestCacheReportsAMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.esc")
	cache := newFileDigestCache()
	_, err := cache.digest(path)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Empty(t, cache.entries)
}
