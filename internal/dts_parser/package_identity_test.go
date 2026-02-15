package dts_parser

import (
	"testing"
)

func TestDerivePackageIdentifier(t *testing.T) {
	tests := []struct {
		name       string
		moduleName string
		expected   string
	}{
		// Simple package names
		{"simple package", "lodash", "lodash"},
		{"simple package underscore", "my_package", "my_package"},

		// Packages with hyphens
		{"hyphenated package", "date-fns", "date_fns"},
		{"multiple hyphens", "my-cool-package", "my_cool_package"},

		// Scoped packages
		{"scoped package", "@types/node", "node"},
		{"scoped package with hyphen", "@types/date-fns", "date_fns"},
		{"custom scope", "@my-scope/my-package", "my_package"},
		{"scope only", "@scope", "scope"},

		// Subpath exports
		{"subpath export", "lodash/fp", "lodash_fp"},
		{"nested subpath", "lodash/fp/map", "lodash_fp_map"},
		{"scoped with subpath", "@types/node/fs", "node_fs"},

		// Packages with dots
		{"dotted package", "socket.io", "socket_io"},
		{"multiple dots", "org.example.package", "org_example_package"},

		// Complex cases
		{"complex scoped subpath", "@my-scope/my-pkg/sub-path", "my_pkg_sub_path"},
		{"all transformations", "@my-scope/my-pkg.v2/sub-path", "my_pkg_v2_sub_path"},

		// Packages starting with digits
		{"starts with digit", "7zip-wrapper", "_7zip_wrapper"},
		{"starts with digit 2", "2fa-auth", "_2fa_auth"},
		{"scoped starts with digit", "@types/7zip", "_7zip"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DerivePackageIdentifier(tt.moduleName)
			if result != tt.expected {
				t.Errorf("DerivePackageIdentifier(%q) = %q, expected %q",
					tt.moduleName, result, tt.expected)
			}
		})
	}
}
