package dts_parser

import (
	"strings"
)

// DerivePackageIdentifier transforms a module/package name (from an import specifier)
// into a valid identifier that can be used as a binding name in Escalier code.
//
// Transformations applied:
//  1. Strip scope prefix (@scope/pkg → pkg)
//  2. Replace hyphens and dots with underscores
//  3. Handle subpath exports (lodash/fp → lodash_fp)
//
// Examples:
//
//	DerivePackageIdentifier("lodash") → "lodash"
//	DerivePackageIdentifier("@types/node") → "node"
//	DerivePackageIdentifier("@scope/my-package") → "my_package"
//	DerivePackageIdentifier("lodash/fp") → "lodash_fp"
//	DerivePackageIdentifier("date-fns") → "date_fns"
//	DerivePackageIdentifier("@my-scope/my-pkg") → "my_pkg"
func DerivePackageIdentifier(moduleName string) string {
	name := moduleName

	// Strip scope prefix (@scope/pkg → pkg)
	if strings.HasPrefix(name, "@") {
		parts := strings.SplitN(name, "/", 2)
		if len(parts) == 2 {
			name = parts[1]
		} else {
			// Edge case: just "@scope" with no package name
			// Remove the @ prefix
			name = strings.TrimPrefix(name, "@")
		}
	}

	// Replace forward slashes with underscores (for subpath exports)
	name = strings.ReplaceAll(name, "/", "_")

	// Replace hyphens with underscores
	name = strings.ReplaceAll(name, "-", "_")

	// Replace dots with underscores
	name = strings.ReplaceAll(name, ".", "_")

	return name
}
