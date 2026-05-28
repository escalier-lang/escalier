package lexer_util

import (
	"testing"
)

func TestScanIdent(t *testing.T) {
	tests := []struct {
		name        string
		contents    string
		startOffset int
		wantValue   string
		wantEnd     int
		wantRunes   int
	}{
		// Basic ASCII identifiers
		{
			name:        "simple lowercase identifier",
			contents:    "hello",
			startOffset: 0,
			wantValue:   "hello",
			wantEnd:     5,
			wantRunes:   5,
		},
		{
			name:        "simple uppercase identifier",
			contents:    "HELLO",
			startOffset: 0,
			wantValue:   "HELLO",
			wantEnd:     5,
			wantRunes:   5,
		},
		{
			name:        "mixed case identifier",
			contents:    "HelloWorld",
			startOffset: 0,
			wantValue:   "HelloWorld",
			wantEnd:     10,
			wantRunes:   10,
		},
		{
			name:        "identifier with numbers",
			contents:    "var123",
			startOffset: 0,
			wantValue:   "var123",
			wantEnd:     6,
			wantRunes:   6,
		},
		{
			name:        "identifier with underscore",
			contents:    "_private",
			startOffset: 0,
			wantValue:   "_private",
			wantEnd:     8,
			wantRunes:   8,
		},
		{
			name:        "identifier with dollar sign",
			contents:    "$jquery",
			startOffset: 0,
			wantValue:   "$jquery",
			wantEnd:     7,
			wantRunes:   7,
		},
		{
			name:        "identifier with underscores and numbers",
			contents:    "my_var_123",
			startOffset: 0,
			wantValue:   "my_var_123",
			wantEnd:     10,
			wantRunes:   10,
		},
		{
			name:        "identifier followed by space",
			contents:    "hello world",
			startOffset: 0,
			wantValue:   "hello",
			wantEnd:     5,
			wantRunes:   5,
		},
		{
			name:        "identifier followed by punctuation",
			contents:    "hello()",
			startOffset: 0,
			wantValue:   "hello",
			wantEnd:     5,
			wantRunes:   5,
		},
		{
			name:        "identifier followed by dot",
			contents:    "obj.property",
			startOffset: 0,
			wantValue:   "obj",
			wantEnd:     3,
			wantRunes:   3,
		},

		// Non-zero start offsets
		{
			name:        "identifier at offset",
			contents:    "let variable = 5",
			startOffset: 4,
			wantValue:   "variable",
			wantEnd:     12,
			wantRunes:   8,
		},
		{
			name:        "second identifier in string",
			contents:    "hello world",
			startOffset: 6,
			wantValue:   "world",
			wantEnd:     11,
			wantRunes:   5,
		},

		// Edge cases - invalid starts
		{
			name:        "starts with number",
			contents:    "123abc",
			startOffset: 0,
			wantValue:   "",
			wantEnd:     0,
			wantRunes:   0,
		},
		{
			name:        "starts with hyphen",
			contents:    "-hello",
			startOffset: 0,
			wantValue:   "",
			wantEnd:     0,
			wantRunes:   0,
		},
		{
			name:        "starts with punctuation",
			contents:    ".hello",
			startOffset: 0,
			wantValue:   "",
			wantEnd:     0,
			wantRunes:   0,
		},
		{
			name:        "empty string",
			contents:    "",
			startOffset: 0,
			wantValue:   "",
			wantEnd:     0,
			wantRunes:   0,
		},
		{
			name:        "offset at end of string",
			contents:    "hello",
			startOffset: 5,
			wantValue:   "",
			wantEnd:     5,
			wantRunes:   0,
		},
		{
			name:        "offset beyond end of string",
			contents:    "hello",
			startOffset: 10,
			wantValue:   "",
			wantEnd:     10,
			wantRunes:   0,
		},

		// Single character identifiers
		{
			name:        "single letter",
			contents:    "a",
			startOffset: 0,
			wantValue:   "a",
			wantEnd:     1,
			wantRunes:   1,
		},
		{
			name:        "single underscore",
			contents:    "_",
			startOffset: 0,
			wantValue:   "_",
			wantEnd:     1,
			wantRunes:   1,
		},
		{
			name:        "single dollar sign",
			contents:    "$",
			startOffset: 0,
			wantValue:   "$",
			wantEnd:     1,
			wantRunes:   1,
		},

		// Unicode identifiers
		{
			name:        "unicode identifier - greek",
			contents:    "αβγ",
			startOffset: 0,
			wantValue:   "αβγ",
			wantEnd:     6,
			wantRunes:   3,
		},
		{
			name:        "unicode identifier - cyrillic",
			contents:    "переменная",
			startOffset: 0,
			wantValue:   "переменная",
			wantEnd:     20,
			wantRunes:   10,
		},
		{
			name:        "unicode identifier - chinese",
			contents:    "变量",
			startOffset: 0,
			wantValue:   "变量",
			wantEnd:     6,
			wantRunes:   2,
		},
		{
			name:        "unicode identifier - arabic",
			contents:    "متغير",
			startOffset: 0,
			wantValue:   "متغير",
			wantEnd:     10,
			wantRunes:   5,
		},
		{
			name:        "mixed ASCII and unicode",
			contents:    "myΔvar",
			startOffset: 0,
			wantValue:   "myΔvar",
			wantEnd:     7,
			wantRunes:   6,
		},
		{
			name:        "unicode with numbers",
			contents:    "var变量123",
			startOffset: 0,
			wantValue:   "var变量123",
			wantEnd:     12,
			wantRunes:   8,
		},
		{
			name:        "unicode identifier followed by space",
			contents:    "переменная = 5",
			startOffset: 0,
			wantValue:   "переменная",
			wantEnd:     20,
			wantRunes:   10,
		},

		// Special cases with $ and _
		{
			name:        "multiple dollar signs",
			contents:    "$$var",
			startOffset: 0,
			wantValue:   "$$var",
			wantEnd:     5,
			wantRunes:   5,
		},
		{
			name:        "multiple underscores",
			contents:    "__proto__",
			startOffset: 0,
			wantValue:   "__proto__",
			wantEnd:     9,
			wantRunes:   9,
		},

		// Identifiers at various positions
		{
			name:        "identifier after whitespace",
			contents:    "   hello",
			startOffset: 3,
			wantValue:   "hello",
			wantEnd:     8,
			wantRunes:   5,
		},
		{
			name:        "identifier in middle of expression",
			contents:    "a + b",
			startOffset: 4,
			wantValue:   "b",
			wantEnd:     5,
			wantRunes:   1,
		},

		// Boundary between ASCII and Unicode
		{
			name:        "ASCII followed by unicode",
			contents:    "hello世界",
			startOffset: 0,
			wantValue:   "hello世界",
			wantEnd:     11,
			wantRunes:   7,
		},
		{
			name:        "unicode start with ASCII continuation",
			contents:    "π123abc",
			startOffset: 0,
			wantValue:   "π123abc",
			wantEnd:     8,
			wantRunes:   7,
		},

		// Long identifiers
		{
			name:        "long ASCII identifier",
			contents:    "thisIsAVeryLongIdentifierNameThatGoesOnAndOn",
			startOffset: 0,
			wantValue:   "thisIsAVeryLongIdentifierNameThatGoesOnAndOn",
			wantEnd:     44,
			wantRunes:   44,
		},

		// Identifiers ending at string boundary
		{
			name:        "identifier at end of string",
			contents:    "test",
			startOffset: 0,
			wantValue:   "test",
			wantEnd:     4,
			wantRunes:   4,
		},
		{
			name:        "unicode identifier at end",
			contents:    "变量",
			startOffset: 0,
			wantValue:   "变量",
			wantEnd:     6,
			wantRunes:   2,
		},

		// Invalid unicode start
		{
			name:        "emoji as start",
			contents:    "😀hello",
			startOffset: 0,
			wantValue:   "",
			wantEnd:     0,
			wantRunes:   0,
		},

		// Real-world JavaScript identifiers
		{
			name:        "camelCase identifier",
			contents:    "getElementById",
			startOffset: 0,
			wantValue:   "getElementById",
			wantEnd:     14,
			wantRunes:   14,
		},
		{
			name:        "PascalCase identifier",
			contents:    "MyComponent",
			startOffset: 0,
			wantValue:   "MyComponent",
			wantEnd:     11,
			wantRunes:   11,
		},
		{
			name:        "SCREAMING_SNAKE_CASE",
			contents:    "MAX_VALUE",
			startOffset: 0,
			wantValue:   "MAX_VALUE",
			wantEnd:     9,
			wantRunes:   9,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotValue, gotEnd, gotRunes := ScanIdent(tt.contents, tt.startOffset)
			if gotValue != tt.wantValue {
				t.Errorf("ScanIdent() value = %q, want %q", gotValue, tt.wantValue)
			}
			if gotEnd != tt.wantEnd {
				t.Errorf("ScanIdent() end = %d, want %d", gotEnd, tt.wantEnd)
			}
			if gotRunes != tt.wantRunes {
				t.Errorf("ScanIdent() runes = %d, want %d", gotRunes, tt.wantRunes)
			}
		})
	}
}

func TestScanIdentNormalization(t *testing.T) {
	// Test cases that require Unicode normalization
	tests := []struct {
		name        string
		contents    string
		startOffset int
		wantValue   string // Expected normalized value
		wantEnd     int
		wantRunes   int
	}{
		{
			name:        "combining characters - e with acute accent",
			contents:    "café", // é as separate combining characters
			startOffset: 0,
			wantValue:   "café", // Should normalize to NFC form
			wantEnd:     5,
			wantRunes:   4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotValue, gotEnd, gotRunes := ScanIdent(tt.contents, tt.startOffset)
			if gotValue != tt.wantValue {
				t.Errorf("ScanIdent() value = %q, want %q", gotValue, tt.wantValue)
			}
			if gotEnd != tt.wantEnd {
				t.Errorf("ScanIdent() end = %d, want %d", gotEnd, tt.wantEnd)
			}
			if gotRunes != tt.wantRunes {
				t.Errorf("ScanIdent() runes = %d, want %d", gotRunes, tt.wantRunes)
			}
		})
	}
}

func TestIsIdentStart(t *testing.T) {
	tests := []struct {
		name string
		r    rune
		want bool
	}{
		// ASCII lowercase letters
		{"lowercase a", 'a', true},
		{"lowercase z", 'z', true},
		{"lowercase m", 'm', true},

		// ASCII uppercase letters
		{"uppercase A", 'A', true},
		{"uppercase Z", 'Z', true},
		{"uppercase M", 'M', true},

		// Underscore and dollar sign
		{"underscore", '_', true},
		{"dollar sign", '$', true},

		// ASCII numbers (invalid start)
		{"digit 0", '0', false},
		{"digit 5", '5', false},
		{"digit 9", '9', false},

		// ASCII punctuation (invalid)
		{"hyphen", '-', false},
		{"dot", '.', false},
		{"comma", ',', false},
		{"semicolon", ';', false},
		{"colon", ':', false},
		{"exclamation", '!', false},
		{"question", '?', false},
		{"at sign", '@', false},
		{"hash", '#', false},
		{"percent", '%', false},
		{"ampersand", '&', false},
		{"asterisk", '*', false},
		{"plus", '+', false},
		{"equals", '=', false},
		{"less than", '<', false},
		{"greater than", '>', false},

		// ASCII whitespace (invalid)
		{"space", ' ', false},
		{"tab", '\t', false},
		{"newline", '\n', false},
		{"carriage return", '\r', false},

		// Unicode letters (valid)
		{"greek alpha", 'α', true},
		{"greek beta", 'β', true},
		{"greek gamma", 'γ', true},
		{"cyrillic а", 'а', true},
		{"cyrillic б", 'б', true},
		{"chinese 变", '变', true},
		{"chinese 量", '量', true},
		{"arabic م", 'م', true},
		{"arabic ت", 'ت', true},
		{"hebrew א", 'א', true},

		// Unicode numbers (invalid start)
		{"unicode digit", '٠', false}, // Arabic-Indic digit zero

		// Emojis (invalid)
		{"emoji grinning", '😀', false},
		{"emoji heart", '❤', false},

		// Other special Unicode
		{"zero width joiner", '\u200D', false},
		{"zero width space", '\u200B', false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsIdentStart(tt.r)
			if got != tt.want {
				t.Errorf("IsIdentStart(%q) = %v, want %v", tt.r, got, tt.want)
			}
		})
	}
}

func TestIsIdentContinue(t *testing.T) {
	tests := []struct {
		name string
		r    rune
		want bool
	}{
		// ASCII lowercase letters
		{"lowercase a", 'a', true},
		{"lowercase z", 'z', true},

		// ASCII uppercase letters
		{"uppercase A", 'A', true},
		{"uppercase Z", 'Z', true},

		// ASCII digits (valid in continuation)
		{"digit 0", '0', true},
		{"digit 5", '5', true},
		{"digit 9", '9', true},

		// Underscore and dollar sign
		{"underscore", '_', true},
		{"dollar sign", '$', true},

		// ASCII punctuation (invalid)
		{"hyphen", '-', false},
		{"dot", '.', false},
		{"comma", ',', false},
		{"semicolon", ';', false},
		{"space", ' ', false},
		{"tab", '\t', false},
		{"newline", '\n', false},

		// Unicode letters (valid)
		{"greek alpha", 'α', true},
		{"cyrillic а", 'а', true},
		{"chinese 变", '变', true},
		{"arabic م", 'م', true},

		// Unicode digits (valid in continuation)
		{"arabic-indic digit", '٥', true},

		// Unicode combining marks (valid in continuation)
		{"combining acute accent", '\u0301', true},
		{"combining diaeresis", '\u0308', true},

		// Emojis (invalid)
		{"emoji", '😀', false},

		// Zero-width characters
		{"zero width joiner", '\u200D', false},
		{"zero width space", '\u200B', false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isIdentContinue(tt.r)
			if got != tt.want {
				t.Errorf("isIdentContinue(%q) = %v, want %v", tt.r, got, tt.want)
			}
		})
	}
}

func TestIsJSDoc(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		// Real JSDoc.
		{"/** doc */", true},
		{"/** */", true},
		{"/**\n * multi\n */", true},
		{"/**foo*/", true},
		// All-asterisk runs carry no content.
		{"/**/", false},
		{"/***/", false},
		{"/****/", false},
		{"/*****/", false},
		// Not a JSDoc opener.
		{"/* doc */", false},
		{"// doc", false},
		{"", false},
		// Unterminated.
		{"/** doc", false},
		{"/**", false},
	}
	for _, tc := range tests {
		if got := IsJSDoc(tc.value); got != tc.want {
			t.Errorf("IsJSDoc(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

func BenchmarkScanIdent(b *testing.B) {
	benchmarks := []struct {
		name     string
		contents string
		offset   int
	}{
		{
			name:     "short ASCII",
			contents: "hello",
			offset:   0,
		},
		{
			name:     "long ASCII",
			contents: "thisIsAVeryLongIdentifierNameThatGoesOnAndOn",
			offset:   0,
		},
		{
			name:     "short unicode",
			contents: "变量",
			offset:   0,
		},
		{
			name:     "mixed ASCII and unicode",
			contents: "myΔvarWithGreekLetter",
			offset:   0,
		},
		{
			name:     "identifier with numbers and underscores",
			contents: "my_var_123_test",
			offset:   0,
		},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				ScanIdent(bm.contents, bm.offset)
			}
		})
	}
}
