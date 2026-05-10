package interop

import (
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sync"
)

//go:embed stdlib
var stdlibFS embed.FS

// blanketPureLibs lists well-known FP/immutable libraries where every member
// defaults to non-mutating at tier 4 unless a specific override exists.
var blanketPureLibs = []string{
	"ramda",
	"fp-ts",
	"effect",
	"immutable",
	"lodash/fp",
}

// NewStdlibRegistry creates an override registry pre-populated with embedded
// stdlib overrides and blanket-pure FP library entries.
func NewStdlibRegistry() (*OverrideRegistry, error) {
	r := newOverrideRegistry()
	if err := loadStdlib(r); err != nil {
		return nil, err
	}
	for _, lib := range blanketPureLibs {
		r.addPureModule(lib)
	}
	return r, nil
}

func loadStdlib(r *OverrideRegistry) error {
	return fs.WalkDir(stdlibFS, "stdlib", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".esc" {
			return nil
		}
		data, err := stdlibFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading embedded stdlib %s: %w", path, err)
		}
		return r.loadSource(string(data), path, false)
	})
}

var (
	defaultRegistryOnce sync.Once
	defaultRegistryVal  *OverrideRegistry
)

// DefaultRegistry returns the process-wide default override registry,
// pre-populated with embedded stdlib overrides and known pure FP libraries.
// Panics if the embedded data fails to parse (should never happen in production).
func DefaultRegistry() *OverrideRegistry {
	defaultRegistryOnce.Do(func() {
		r, err := NewStdlibRegistry()
		if err != nil {
			panic(fmt.Sprintf("interop: loading embedded stdlib overrides: %v", err))
		}
		defaultRegistryVal = r
	})
	return defaultRegistryVal
}
