package solver

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestFileDigestCache covers when digest reuses a recorded digest and when it reads
// the file again. Each row writes a file, takes its digest, changes the file, and
// takes the digest again. A reuse shows as the first contents' digest coming back
// for the second contents.
//
// Rewriting a file with contents of the same size and setting the old modification
// time back leaves the stamp unchanged, which is the case the stamp cannot tell
// from an unchanged file.
func TestFileDigestCache(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	later := past.Add(time.Minute)
	cases := []struct {
		name   string
		first  string
		second string
		// firstAt and secondAt are the modification times the file is given after
		// each write. A zero time leaves the time the write gave it.
		firstAt, secondAt time.Time
		wantReuse         bool
	}{
		{
			name: "unchanged stamp reuses the digest", first: "abc", second: "xyz",
			firstAt: past, secondAt: past, wantReuse: true,
		},
		{
			name: "changed size reads the file again", first: "abc", second: "abcd",
			firstAt: past, secondAt: past,
		},
		{
			name: "changed modification time reads the file again", first: "abc", second: "xyz",
			firstAt: past, secondAt: later,
		},
		{
			name: "a file written within the racy window is not recorded", first: "abc", second: "xyz",
			secondAt: time.Time{},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "file.esc")
			write := func(contents string, at time.Time) {
				require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
				if !at.IsZero() {
					require.NoError(t, os.Chtimes(path, at, at))
				}
			}
			cache := newFileDigestCache()

			write(tt.first, tt.firstAt)
			first, err := cache.digest(path)
			require.NoError(t, err)
			require.Equal(t, sha256.Sum256([]byte(tt.first)), first)

			// The racy-window row keeps the write's own time for both writes, so it
			// sets the first one back to the same time as the second.
			if tt.firstAt.IsZero() {
				info, err := os.Stat(path)
				require.NoError(t, err)
				write(tt.second, info.ModTime())
			} else {
				write(tt.second, tt.secondAt)
			}
			second, err := cache.digest(path)
			require.NoError(t, err)
			if tt.wantReuse {
				require.Equal(t, first, second)
			} else {
				require.Equal(t, sha256.Sum256([]byte(tt.second)), second)
			}
		})
	}
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
