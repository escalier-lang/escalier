package solver

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/escalier-lang/escalier/internal/set"
)

// jsSpaceSet is the members of JavaScript's `\s` class, written as the inside of a Go
// character class. Go's own `\s` holds only the ASCII spaces.
const jsSpaceSet = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// jsDotClass is what JavaScript's `.` matches without the `s` flag, every character but a
// line terminator. Go's own `.` excludes only `\n`.
const jsDotClass = `[^\n\r\x{2028}\x{2029}]`

// compileRegexType compiles a regex annotation, written as a JavaScript regex literal such as
// `/^\d{3}$/i`, for Go's RE2 engine. It returns the compiled pattern, or nil and the reason
// the annotation cannot be a type.
//
// A regex type is matched while the program is checked, so a pattern that backtracks would
// hang the checker. RE2 matches in time linear in its input, which rules that out, and it
// supports no construct that needs backtracking. Lookahead, lookbehind and backreferences are
// such constructs. Each one is named in the reason rather than left to the compiler, whose
// message would blame the rewrite below instead of the construct the writer used.
//
// The flags translate this way:
//
//   - `i`, `m` and `s` become a leading Go flag group.
//   - `y` anchors the match at the start, as a sticky regex tested from index 0 is.
//   - `u` changes how `\u` escapes read, which rewriteRegexBody follows.
//   - `g` and `d` change nothing a whole-string test reads, so they are dropped.
//   - `v` is rejected, since its set notation has no RE2 form.
//
// rewriteRegexBody translates the body.
func compileRegexType(source string) (*regexp.Regexp, string) {
	if len(source) < 2 || source[0] != '/' {
		return nil, "is not a regex literal"
	}
	end := strings.LastIndexByte(source, '/')
	if end == 0 {
		return nil, "is not a regex literal"
	}
	body, flags := source[1:end], source[end+1:]

	var goFlags strings.Builder
	sticky, dotAll, unicode := false, false, false
	seen := set.NewSet[rune]()
	for _, flag := range flags {
		if seen.Contains(flag) {
			return nil, "repeats the flag `" + string(flag) + "`"
		}
		seen.Add(flag)
		switch flag {
		case 'i', 'm':
			goFlags.WriteRune(flag)
		case 's':
			goFlags.WriteRune(flag)
			dotAll = true
		case 'y':
			sticky = true
		case 'u':
			unicode = true
		case 'g', 'd':
		case 'v':
			return nil, "uses the `v` flag, whose set notation RE2 does not support"
		default:
			return nil, "uses the unknown flag `" + string(flag) + "`"
		}
	}

	pattern, reason := rewriteRegexBody(body, dotAll, unicode)
	if reason != "" {
		return nil, reason
	}
	if sticky {
		pattern = `\A(?:` + pattern + `)`
	}
	if goFlags.Len() > 0 {
		pattern = "(?" + goFlags.String() + ")" + pattern
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, "does not compile: " + err.Error()
	}
	return compiled, ""
}

// rewriteRegexBody rewrites the body of a JavaScript regex into Go syntax, or returns why it
// cannot. dotAll reports whether the `s` flag is set, and unicode whether the `u` flag is. The body is read one token at a time,
// so an escaped `\(` and a `(` inside a character class are never taken for a group.
//
// Where JavaScript and Go read the same syntax differently, the JavaScript meaning is
// written out:
//
//   - `\s` and `\S` hold JavaScript's Unicode spaces, and `.` excludes every line terminator.
//   - `[]` matches nothing and `[^]` matches any character.
//   - `\uXXXX`, `\cX` and `\0` become Go's `\x{…}` escapes, and so does `\u{X…}` under the
//     `u` flag. Without it, a `\u` that starts no `\uXXXX` escape is a literal `u`, so
//     `\u{42}` is `u` repeated 42 times.
//   - A named group `(?<name>` becomes `(?P<name>`.
func rewriteRegexBody(body string, dotAll, unicode bool) (string, string) {
	var out strings.Builder
	inClass := false
	for i := 0; i < len(body); i++ {
		ch := body[i]
		switch {
		case ch == '\\' && i+1 < len(body):
			escape, width, reason := rewriteRegexEscape(body[i:], inClass, unicode)
			if reason != "" {
				return "", reason
			}
			out.WriteString(escape)
			i += width - 1
		case inClass:
			if ch == ']' {
				inClass = false
			}
			out.WriteByte(ch)
		case strings.HasPrefix(body[i:], "[]"):
			out.WriteString(`[^\x{0}-\x{10ffff}]`)
			i++
		case strings.HasPrefix(body[i:], "[^]"):
			out.WriteString(`[\x{0}-\x{10ffff}]`)
			i += 2
		case ch == '[':
			inClass = true
			out.WriteByte(ch)
		case ch == '.' && !dotAll:
			out.WriteString(jsDotClass)
		case strings.HasPrefix(body[i:], "(?<=") || strings.HasPrefix(body[i:], "(?<!"):
			return "", "uses lookbehind, which RE2 does not support"
		case strings.HasPrefix(body[i:], "(?=") || strings.HasPrefix(body[i:], "(?!"):
			return "", "uses lookahead, which RE2 does not support"
		case strings.HasPrefix(body[i:], "(?<"):
			out.WriteString("(?P<")
			i += len("(?<") - 1
		default:
			out.WriteByte(ch)
		}
	}
	return out.String(), ""
}

// rewriteRegexEscape rewrites the escape at the start of rest, which begins with a
// backslash. It returns the Go spelling, how many bytes of rest the escape spans, and a
// reason when the escape cannot be written in RE2. inClass reports whether the escape sits
// inside a character class, and unicode whether the `u` flag is set.
func rewriteRegexEscape(rest string, inClass, unicode bool) (string, int, string) {
	next := rest[1]
	switch {
	case next >= '1' && next <= '9' && !inClass:
		return "", 0, "uses a backreference, which RE2 does not support"
	case next == 'k' && strings.HasPrefix(rest[2:], "<") && !inClass:
		return "", 0, "uses a named backreference, which RE2 does not support"
	case next == 's':
		if inClass {
			return jsSpaceSet, 2, ""
		}
		return "[" + jsSpaceSet + "]", 2, ""
	case next == 'S':
		if inClass {
			return "", 0, "uses `\\S` inside a character class, which has no RE2 form"
		}
		return "[^" + jsSpaceSet + "]", 2, ""
	case next == 'u' && unicode && strings.HasPrefix(rest[2:], "{"):
		end := strings.IndexByte(rest, '}')
		if end < 0 {
			return "", 0, "uses an unterminated `\\u{` escape"
		}
		return `\x{` + rest[3:end] + `}`, end + 1, ""
	case next == 'u' && len(rest) >= 6 && isHex(rest[2:6]):
		return `\x{` + rest[2:6] + `}`, 6, ""
	case next == 'u' && !unicode:
		return "u", 2, ""
	case next == 'c' && len(rest) >= 3 && isASCIILetter(rest[2]):
		return fmt.Sprintf(`\x{%x}`, rest[2]%32), 3, ""
	case next == '0' && (len(rest) < 3 || rest[2] < '0' || rest[2] > '9'):
		return `\x{0}`, 2, ""
	default:
		return rest[:2], 2, ""
	}
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func isASCIILetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
