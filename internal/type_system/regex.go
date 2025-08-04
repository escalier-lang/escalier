package type_system

import (
	"fmt"
	"strings"
)

// convertJSRegexToGo converts a JavaScript regex literal to Go regex syntax
// Input format: /pattern/flags (e.g., "/hello/gi", "/^\d+$/")
// Output: Go-compatible regex pattern
func convertJSRegexToGo(jsRegex string) (string, error) {
	// Remove leading and trailing slashes
	if len(jsRegex) < 2 || jsRegex[0] != '/' {
		return "", fmt.Errorf("invalid regex format: %s", jsRegex)
	}

	// Find the closing slash
	lastSlash := strings.LastIndex(jsRegex[1:], "/")
	if lastSlash == -1 {
		return "", fmt.Errorf("invalid regex format: %s", jsRegex)
	}
	lastSlash++ // Adjust for the slice offset

	pattern := jsRegex[1:lastSlash]
	flags := ""
	if lastSlash+1 < len(jsRegex) {
		flags = jsRegex[lastSlash+1:]
	}

	// Convert flags to Go format
	var goFlags []string
	var multiline, dotAll, caseInsensitive bool

	for _, flag := range flags {
		switch flag {
		case 'i':
			caseInsensitive = true
		case 'm':
			multiline = true
		case 's':
			dotAll = true
		case 'g':
			// Global flag doesn't apply to MatchString, ignore
		case 'u':
			// Unicode flag is default in Go, ignore
		case 'y':
			// Sticky flag not supported in Go, ignore for now
		default:
			// Unknown flag, ignore
		}
	}

	// Apply flags using Go syntax
	if caseInsensitive {
		goFlags = append(goFlags, "i")
	}
	if multiline {
		goFlags = append(goFlags, "m")
	}
	if dotAll {
		goFlags = append(goFlags, "s")
	}

	// Build the final pattern
	result := pattern

	// Convert JavaScript named capture groups to Go format
	// JavaScript: (?<name>...) -> Go: (?P<name>...)
	result = strings.ReplaceAll(result, "(?<", "(?P<")

	if len(goFlags) > 0 {
		result = "(?" + strings.Join(goFlags, "") + ")" + result
	}

	return result, nil
}
