package compiler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/escalier-lang/escalier/internal/set"
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
	// Dependencies holds the packages the package.json lists under `dependencies`,
	// `peerDependencies` and `optionalDependencies`. An import with no URI scheme
	// resolves only when it names one of them. `devDependencies` are left out, since
	// a published package's own code cannot rely on them being installed. It is nil
	// in the zero value, which lets such an import name any package.
	Dependencies set.Set[string]
}

// packageJSON is the shape of the package.json fields ReadPackageConfig reads.
type packageJSON struct {
	Escalier struct {
		Ambient *bool `json:"ambient"`
	} `json:"escalier"`
	Dependencies         map[string]string `json:"dependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
}

// ReadPackageConfig reads the PackageConfig from the package.json in dir, and records dir
// as its absolute Dir. A package.json with no `escalier` field gives the defaults. A
// directory with no package.json, or a package.json that does not parse, also lists no
// Dependencies. It returns an error when the file cannot be read or does not parse.
func ReadPackageConfig(dir string) (PackageConfig, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return PackageConfig{}, err
	}
	path := filepath.Join(absDir, "package.json")
	contents, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return PackageConfig{Dir: absDir, Dependencies: set.NewSet[string]()}, nil
	}
	if err != nil {
		return PackageConfig{}, err
	}
	var pkg packageJSON
	if err := json.Unmarshal(contents, &pkg); err != nil {
		// Dir is still known, so a caller that goes on with the defaults resolves the
		// package's imports.
		return PackageConfig{Dir: absDir, Dependencies: set.NewSet[string]()}, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	config := PackageConfig{Dir: absDir, Dependencies: set.NewSet[string]()}
	for _, deps := range []map[string]string{pkg.Dependencies, pkg.PeerDependencies, pkg.OptionalDependencies} {
		for name := range deps {
			config.Dependencies.Add(name)
		}
	}
	if pkg.Escalier.Ambient != nil {
		config.NoAmbientNames = !*pkg.Escalier.Ambient
	}
	return config, nil
}
