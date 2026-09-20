package stdlibdir

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// package_file.go maps between a pseudo-package URI and the `.esc` file that
// declares it.
//
// A package's file is named for the package, optionally followed by the
// environments its declarations run on: `web/dom.window.esc` declares
// `web:dom` and says a page is the only place it exists. A file with no
// suffix runs everywhere. What a suffix means is the converter's to read;
// a caller resolving an import only has to find the file, which is why the
// vocabulary lives in internal/dts_to_esc and not here.
//
// A package name is lowercase letters, digits and underscores, so the first
// `.` in a basename always begins the suffix.

// PackageName returns the package a `.esc` basename declares, with any
// environment suffix dropped. It reports false for a name that is not a
// `.esc` file.
func PackageName(basename string) (string, bool) {
	if !strings.HasSuffix(basename, ".esc") {
		return "", false
	}
	stem := strings.TrimSuffix(basename, ".esc")
	if name, _, found := strings.Cut(stem, "."); found {
		return name, name != ""
	}
	return stem, stem != ""
}

// PackageFile returns the path of the file under dir/scheme that declares
// scheme:pkg. It reports false when no file declares it.
//
// An unsuffixed file wins over a suffixed one, and two suffixed files are an
// error, since nothing says which of them the import meant. The generator is
// what keeps a package to one file: it writes each package under the name the
// partition table gives it and removes any other file claiming that package,
// so a half-finished rename does not survive a regeneration.
func PackageFile(dir, scheme, pkg string) (string, bool, error) {
	schemeDir := filepath.Join(dir, scheme)
	// A package with no suffix is the common case, and every `std:*` package is
	// one, so answer it without listing the directory. Only a package whose
	// name carries environments costs a scan.
	plain := filepath.Join(schemeDir, pkg+".esc")
	if info, err := os.Stat(plain); err == nil && !info.IsDir() {
		return plain, true, nil
	}
	entries, err := os.ReadDir(schemeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("cannot scan %s: %w", schemeDir, err)
	}
	var found string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name, ok := PackageName(entry.Name())
		if !ok || name != pkg {
			continue
		}
		if found != "" {
			return "", false, fmt.Errorf(
				"%s:%s is declared by both %s and %s under %s",
				scheme, pkg, filepath.Base(found), entry.Name(), schemeDir)
		}
		found = filepath.Join(schemeDir, entry.Name())
	}
	return found, found != "", nil
}
