package lexer_util

import (
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Based on https://www.unicode.org/reports/tr31/#D1
func IsIdentStart(r rune) bool {
	// ASCII fast path
	if r < 128 {
		return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || r == '$'
	}
	// Non-ASCII, use full Unicode check
	return (r == '_' || r == '$' || // '_', '$' are not included in the UAX-31 spec
		unicode.IsLetter(r) ||
		unicode.Is(unicode.Nl, r) ||
		unicode.Is(unicode.Other_ID_Start, r)) &&
		!unicode.Is(unicode.Pattern_Syntax, r) &&
		!unicode.Is(unicode.Pattern_White_Space, r)
}

// Based on https://www.unicode.org/reports/tr31/#D1
func isIdentContinue(r rune) bool {
	// ASCII fast path
	if r < 128 {
		return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '$'
	}
	// Non-ASCII, use full Unicode check
	return (r == '_' || r == '$' || // '_', '$' are not included in the UAX-31 spec
		unicode.IsLetter(r) ||
		unicode.Is(unicode.Nl, r) ||
		unicode.Is(unicode.Other_ID_Start, r) ||
		unicode.Is(unicode.Mn, r) ||
		unicode.Is(unicode.Mc, r) ||
		unicode.Is(unicode.Nd, r) ||
		unicode.Is(unicode.Pc, r) ||
		unicode.Is(unicode.Other_ID_Continue, r)) &&
		!unicode.Is(unicode.Pattern_Syntax, r) &&
		!unicode.Is(unicode.Pattern_White_Space, r)
}

// scanIdentContinuation scans identifier continuation characters starting at offset i.
// Returns the updated offset and rune count.
func scanIdentContinuation(contents string, i int, runeCount int) (int, int) {
	n := len(contents)
	for i < n {
		// Fast check for ASCII continuation
		if contents[i] <= 127 {
			c := contents[i]
			if !((c >= 'a' && c <= 'z') ||
				(c >= 'A' && c <= 'Z') ||
				(c >= '0' && c <= '9') ||
				c == '_' || c == '$') {
				break
			}
			i++
			runeCount++
			continue
		}

		// Unicode path
		codePoint, width := utf8.DecodeRuneInString(contents[i:])
		if !isIdentContinue(codePoint) {
			break // trigger on unicode operators or punctuation
		}
		i += width
		runeCount++
	}
	return i, runeCount
}

// scanIdent scans an identifier starting at the given offset and returns the normalized value,
// the ending offset, and the rune count. Returns empty string, start offset, and 0 if not a valid identifier.
func ScanIdent(contents string, startOffset int) (string, int, int) {
	n := len(contents)

	if startOffset >= n {
		return "", startOffset, 0
	}

	// Fast path for ASCII identifiers (most common case)
	firstChar := contents[startOffset]
	if firstChar <= 127 {
		// Check ASCII identifier start: a-z, A-Z, _, $
		if !((firstChar >= 'a' && firstChar <= 'z') ||
			(firstChar >= 'A' && firstChar <= 'Z') ||
			firstChar == '_' || firstChar == '$') {
			return "", startOffset, 0
		}

		// Scan ASCII identifier continuation: a-z, A-Z, 0-9, _, $
		i := startOffset + 1
		runeCount := 1
		for i < n && contents[i] <= 127 {
			c := contents[i]
			if !((c >= 'a' && c <= 'z') ||
				(c >= 'A' && c <= 'Z') ||
				(c >= '0' && c <= '9') ||
				c == '_' || c == '$') {
				break
			}
			i++
			runeCount++
		}

		// If we scanned to the end or hit ASCII non-identifier char, we're done (no normalization needed)
		if i >= n || contents[i] <= 127 {
			return contents[startOffset:i], i, runeCount
		}

		// We hit a Unicode character - continue scanning from where we left off
		needsNormalization := true
		i, runeCount = scanIdentContinuation(contents, i, runeCount)

		value := contents[startOffset:i]
		if needsNormalization {
			value = string(norm.NFC.Bytes([]byte(value)))
		}
		return value, i, runeCount
	}

	// Slow path for Unicode identifiers starting with Unicode character
	codePoint, width := utf8.DecodeRuneInString(contents[startOffset:])

	// Check if it starts with a valid identifier start character
	if !IsIdentStart(codePoint) {
		return "", startOffset, 0
	}

	// Scan the full identifier and track if normalization is needed
	i := startOffset + width
	runeCount := 1
	needsNormalization := true

	i, runeCount = scanIdentContinuation(contents, i, runeCount)

	value := contents[startOffset:i]
	// Only normalize if we found non-ASCII characters
	if needsNormalization {
		value = string(norm.NFC.Bytes([]byte(value)))
	}
	return value, i, runeCount
}
