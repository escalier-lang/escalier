package solver

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// stdlib_digest_cache.go keeps the SHA-256 of each stdlib file ambientKeyOf hashes,
// so a run whose files have not changed stats them rather than reading and hashing
// them again.
//
// An entry is keyed by the file's absolute path and holds the size and
// modification time the file was read under. A stat that reports the same pair
// reuses the digest. The same racy-write rule packageGraphCache follows applies: a
// file modified within racyWindow of the read is hashed but not recorded, so a
// later same-size edit can only produce a modification time the cache has not seen.

// fileDigestCache holds one digest per file path.
type fileDigestCache struct {
	mu      sync.Mutex
	entries map[string]fileDigestEntry
}

// fileDigestEntry is one file's digest and the stamp it was read under.
type fileDigestEntry struct {
	size    int64
	modTime time.Time
	digest  [sha256.Size]byte
}

func newFileDigestCache() *fileDigestCache {
	return &fileDigestCache{entries: map[string]fileDigestEntry{}}
}

// fileDigests is the process-wide cache ambientKeyOf reads.
var fileDigests = newFileDigestCache()

// digest returns the SHA-256 of the file at path.
func (c *fileDigestCache) digest(path string) ([sha256.Size]byte, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("cannot resolve %s: %w", path, err)
	}
	// The stat comes before the read, so the contents hashed are at least as new as
	// the stamp they are recorded under. A write between the two leaves a stamp
	// older than the file, which the next stat does not match.
	info, err := os.Stat(abs)
	if err != nil {
		return [sha256.Size]byte{}, err
	}

	c.mu.Lock()
	entry, held := c.entries[abs]
	c.mu.Unlock()
	if held && entry.size == info.Size() && entry.modTime.Equal(info.ModTime()) {
		return entry.digest, nil
	}

	contents, err := os.ReadFile(abs)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	digest := sha256.Sum256(contents)

	c.mu.Lock()
	defer c.mu.Unlock()
	if info.ModTime().Before(time.Now().Add(-racyWindow)) {
		c.entries[abs] = fileDigestEntry{size: info.Size(), modTime: info.ModTime(), digest: digest}
	} else {
		delete(c.entries, abs)
	}
	return digest, nil
}
