package solver

import (
	"regexp"
	"strings"
)

// compileRegexType compiles a regex annotation, written as a JavaScript regex literal such as
// `/^\d{3}$/i`, for Go's RE2 engine. It returns the compiled pattern, or nil and the reason
// the annotation cannot be a type.
//
// A regex type is matched while the program is checked, so a pattern that backtracks would
// hang the checker. RE2 matches in time linear in its input, which rules that out, and it
// supports no construct that needs backtracking. Lookahead, lookbehind and backreferences are
// such constructs. Each one is named in the reason rather than left to the compiler, whose
// message would blame the syntax rewrite below instead of the construct the writer used.
//
// The rewrite turns JavaScript syntax into Go's:
//
//   - The `i`, `m` and `s` flags become a leading Go flag group.
//   - The `y` flag anchors the match at the start, as a sticky regex tested from index 0 is.
//   - A named group `(?<name>` becomes `(?P<name>`.
//
// The `g`, `u`, `v` and `d` flags change nothing a whole-string test reads, so they are dropped.
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
	sticky := false
	for _, flag := range flags {
		switch flag {
		case 'i', 'm', 's':
			goFlags.WriteRune(flag)
		case 'y':
			sticky = true
		case 'g', 'u', 'v', 'd':
		default:
			return nil, "uses the unknown flag `" + string(flag) + "`"
		}
	}

	pattern, reason := rewriteRegexBody(body)
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

// rewriteRegexBody rewrites the body of a JavaScript regex into Go syntax, or returns the
// name of a construct RE2 does not support. It reads the body one token at a time, so an
// escaped `\(` and a `(` inside a character class are never taken for a group.
func rewriteRegexBody(body string) (string, string) {
	var out strings.Builder
	inClass := false
	for i := 0; i < len(body); i++ {
		ch := body[i]
		switch {
		case ch == '\\' && i+1 < len(body):
			next := body[i+1]
			if !inClass && next >= '1' && next <= '9' {
				return "", "uses a backreference, which RE2 does not support"
			}
			if !inClass && next == 'k' && i+2 < len(body) && body[i+2] == '<' {
				return "", "uses a named backreference, which RE2 does not support"
			}
			out.WriteByte(ch)
			out.WriteByte(next)
			i++
		case inClass:
			if ch == ']' {
				inClass = false
			}
			out.WriteByte(ch)
		case ch == '[':
			inClass = true
			out.WriteByte(ch)
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
