package compiler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// PackageConfig is the part of a package's package.json the compiler reads. The zero
// value is the configuration of a package that sets nothing.
type PackageConfig struct {
	// NoAmbientNames leaves the builtins unbound unless a file imports them, so
	// `Math.PI` needs `import "std:math"` and is spelled `math.PI`. It is set by
	// `"escalier": {"ambient": false}`. Only internal/solver reads it.
	NoAmbientNames bool
	// Dir is the package's root directory, where its package.json sits. An import
	// with no URI scheme resolves through the `node_modules` directories at and
	// above it.
	Dir string
}

// packageJSON is the shape of the package.json fields ReadPackageConfig reads.
type packageJSON struct {
	Escalier struct {
		Ambient *bool `json:"ambient"`
	} `json:"escalier"`
}

// ReadPackageConfig reads the PackageConfig from the package.json in dir, and records dir
// as its absolute Dir. A directory with no package.json, or a package.json with no
// `escalier` field, gives the defaults. It returns an error when the file cannot be read
// or does not parse.
func ReadPackageConfig(dir string) (PackageConfig, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return PackageConfig{}, err
	}
	path := filepath.Join(absDir, "package.json")
	contents, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return PackageConfig{Dir: absDir}, nil
	}
	if err != nil {
		return PackageConfig{}, err
	}
	var pkg packageJSON
	if err := json.Unmarshal(contents, &pkg); err != nil {
		return PackageConfig{}, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	config := PackageConfig{Dir: absDir}
	if pkg.Escalier.Ambient != nil {
		config.NoAmbientNames = !*pkg.Escalier.Ambient
	}
	return config, nil
}
