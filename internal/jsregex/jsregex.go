// Package jsregex compiles a JavaScript regex literal for Go's RE2 engine and matches strings
// against it the way JavaScript would.
//
// RE2 matches in time linear in its input, so a pattern checked while a program is type-checked
// cannot hang the checker however it is written. It supports no construct that needs
// backtracking, so Compile rejects lookahead, lookbehind and backreferences by name.
//
// JavaScript and Go read some of the same syntax differently. Compile rewrites the pattern so
// a match agrees with JavaScript:
//
//   - `\s` and `\S` hold JavaScript's Unicode spaces, and `.` excludes every line terminator.
//   - `[]` matches nothing, `[^]` matches any character, and `[\b]` matches a backspace.
//   - Without the `u` flag, an escape JavaScript gives no meaning, such as `\a` or `\p`, is
//     the character it escapes.
//   - A named group `(?<name>` becomes `(?P<name>`.
//
// A code point is the number Unicode gives one character, such as U+0061 for `a` or U+1F600
// for 😀. A code unit is one 16-bit piece of a string's UTF-16 encoding, the form JavaScript
// strings take. A code point up to U+FFFF fits in one code unit. One above U+FFFF, called an
// astral character, takes two code units, called a surrogate pair. So "😀".length is 2 in
// JavaScript.
//
// Without the `u` flag JavaScript matches code units, where RE2 matches code points. A Pattern
// compiled without it therefore matches a string transcoded to one rune per code unit. Each
// surrogate unit maps to a rune in a private-use range no transcoded string holds otherwise,
// and the pattern's surrogate escapes and astral characters map the same way. So `/^..$/`
// matches "😀" and `/^.$/` does not, as in JavaScript.
package jsregex

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"strings"
	"sync"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/escalier-lang/escalier/internal/set"
)

// spaceSet is the members of JavaScript's `\s` class, written as the inside of a Go
// character class. Go's own `\s` holds only the ASCII spaces.
const spaceSet = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// dotClass is what JavaScript's `.` matches without the `s` flag, every character but a line
// terminator. Go's own `.` excludes only `\n`.
const dotClass = `[^\n\r\x{2028}\x{2029}]`

// unitBase is the first rune of the private-use range a surrogate code unit maps to when a
// pattern matches code units. Plane 15 holds no character a transcoded string carries as a
// rune, since every astral character is split into its two units first.
const unitBase = 0xF0000

// Pattern is a compiled JavaScript regex.
type Pattern struct {
	re *regexp.Regexp
	// units reports whether the pattern matches UTF-16 code units, which it does without the
	// `u` flag.
	units bool
	// flags holds the literal's flags as written, such as "iu".
	flags string
	// body is the literal's source between its slashes.
	body string
	// goBody is body rewritten into RE2 syntax, without the flag group goFlags holds.
	goBody string
	// goFlags holds the Go flags the literal's flags map to, such as "im".
	goFlags string
	// groupSpans holds where in body each group of each name sits, in the order of the group's
	// Indices.
	groupSpans map[string][]groupSpan
	// Groups holds each named capture group, in source order.
	Groups []Group

	shadowOnce sync.Once
	shadowed   []Shadowed

	searchOnce sync.Once
	search     *syntax.Prog
}

// Group is one name a pattern's named capture groups use.
type Group struct {
	Name string
	// Indices holds the subexpression index of every group that uses the name. JavaScript
	// allows one name on groups in different alternatives, and a match reports the one that
	// took part.
	Indices []int
	// Optional reports whether a match can leave every group with this name out, as `(?<b>b)?`
	// or one side of an alternation does.
	Optional bool
	// Bodies holds the JavaScript source between the parentheses of every group that uses the
	// name, in the order of Indices. The body of `(?<y>\d{4})` is `\d{4}`.
	Bodies []string
}

// Compile compiles a JavaScript regex literal such as `/^\d{3}$/i`. It returns nil and the
// reason when the literal cannot be compiled, phrased to follow the literal in a sentence:
// "uses lookahead, which RE2 does not support".
//
// The flags translate this way:
//
//   - `i`, `m` and `s` become a leading Go flag group.
//   - `y` anchors the match at the start, as a sticky regex tested from index 0 is.
//   - `u` makes the pattern match code points rather than code units.
//   - `g` and `d` change nothing a whole-string test reads, so they are dropped.
//   - `v` is rejected, since its set notation has no RE2 form.
func Compile(source string) (*Pattern, string) {
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

	r := rewriter{dotAll: dotAll, unicode: unicode}
	pattern, reason := r.body(body)
	if reason != "" {
		return nil, reason
	}
	if sticky {
		pattern = `\A(?:` + pattern + `)`
	}
	goBody := pattern
	if goFlags.Len() > 0 {
		pattern = "(?" + goFlags.String() + ")" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, "does not compile: " + err.Error()
	}
	groups := groupsOf(re, pattern)
	spans := groupSpans(body)
	for i := range groups {
		for _, gs := range spans[groups[i].Name] {
			groups[i].Bodies = append(groups[i].Bodies, gs.body.text(body))
		}
	}
	return &Pattern{
		re:         re,
		units:      !unicode,
		flags:      flags,
		body:       body,
		goBody:     goBody,
		goFlags:    goFlags.String(),
		groupSpans: spans,
		Groups:     groups,
	}, ""
}

// CaptureSources returns a regex literal for each group named by g, in the order of
// g.Indices. Each literal matches every string its group can capture, so `(?<y>\d{4})` gives
// `/^\d{4}$/`. The text around a group can rule out some of those strings, so a literal can
// match more than the group ever captures.
//
// An alternative Shadowed reports is left out of the literal, since it never takes part in a
// match, so `(?<a>a|ab).*` gives `/^a$/`. A group that sits inside such an alternative never
// captures anything and gives no literal.
//
// It reports false when a group's body tests the text around the group, which a literal of
// the body alone cannot read. A body does that through `\b` or `\B`, or through `^` or `$`
// under the `m` flag, where they test for a line terminator before or after the group.
func (p *Pattern) CaptureSources(g Group) ([]string, bool) {
	multiline := strings.ContainsRune(p.flags, 'm')
	// The `g`, `d` and `y` flags change nothing a whole-string test reads, and `m` would make
	// the literal's own anchors match at line terminators.
	flags := strings.Map(func(r rune) rune {
		if strings.ContainsRune("isu", r) {
			return r
		}
		return -1
	}, p.flags)
	sources := []string{}
	for _, gs := range p.groupSpans[g.Name] {
		body, live := p.liveBody(gs)
		if !live {
			continue
		}
		if readsSurroundings(body, multiline) {
			return nil, false
		}
		if hasTopLevelAlternation(body) {
			body = "(?:" + body + ")"
		}
		sources = append(sources, "/^"+body+"$/"+flags)
	}
	return sources, true
}

// liveBody returns the body of the group at gs with every shadowed alternative inside it
// removed, along with the `|` before it. It reports false when the group itself sits inside a
// shadowed alternative.
func (p *Pattern) liveBody(gs groupSpan) (string, bool) {
	var b strings.Builder
	at := gs.body.start
	for _, s := range p.Shadowed() {
		// Shadowed spans count from the literal's opening slash, and body offsets do not.
		dead := span{s.Start - 1, s.End - 1}
		if dead.start <= gs.open && gs.open < dead.end {
			return "", false
		}
		if dead.start < gs.body.start || dead.end > gs.body.end {
			continue
		}
		// A shadowed alternative is never the first of its alternation, so a `|` comes before it.
		b.WriteString(p.body[at : dead.start-1])
		at = dead.end
	}
	b.WriteString(p.body[at:gs.body.end])
	return b.String(), true
}

// forEachToken calls visit with the start and source of each token of the JavaScript regex
// body, and whether the token sits inside a character class. An escape is one token of its
// backslash and the character after it, and every other byte is a token of its own.
func forEachToken(body string, visit func(i int, tok string, inClass bool)) {
	inClass := false
	for i := 0; i < len(body); {
		width := 1
		if body[i] == '\\' && i+1 < len(body) {
			width = 2
		}
		tok := body[i : i+width]
		switch {
		case tok == "[" && !inClass:
			visit(i, tok, false)
			inClass = true
		case tok == "]" && inClass:
			inClass = false
			visit(i, tok, false)
		default:
			visit(i, tok, inClass)
		}
		i += width
	}
}

// groupSpan is where a named group sits in a regex body.
type groupSpan struct {
	// open is the offset of the group's `(`.
	open int
	// body spans the source between the group's name and its `)`.
	body span
}

// groupSpans returns where each named group sits in body, keyed by name, in the order the
// groups open.
func groupSpans(body string) map[string][]groupSpan {
	type frame struct {
		open, start int
		// slot is the group's position among the named groups, and -1 for any other group.
		slot int
	}
	var stack []frame
	var names []string
	var spans []groupSpan
	forEachToken(body, func(i int, tok string, inClass bool) {
		if inClass {
			return
		}
		switch tok {
		case "(":
			rest := body[i:]
			if !strings.HasPrefix(rest, "(?<") || strings.HasPrefix(rest, "(?<=") || strings.HasPrefix(rest, "(?<!") {
				stack = append(stack, frame{slot: -1})
				return
			}
			end := strings.IndexByte(rest, '>')
			stack = append(stack, frame{open: i, start: i + end + 1, slot: len(names)})
			names = append(names, rest[3:end])
			spans = append(spans, groupSpan{})
		case ")":
			if len(stack) == 0 {
				return
			}
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if top.slot >= 0 {
				spans[top.slot] = groupSpan{open: top.open, body: span{top.start, i}}
			}
		}
	})
	out := map[string][]groupSpan{}
	for i, name := range names {
		out[name] = append(out[name], spans[i])
	}
	return out
}

// readsSurroundings reports whether body tests the characters next to the text it matches.
// It does through `\b` or `\B`, and through `^` or `$` when multiline makes them line anchors.
func readsSurroundings(body string, multiline bool) bool {
	reads := false
	forEachToken(body, func(_ int, tok string, inClass bool) {
		if inClass {
			return
		}
		switch tok {
		case `\b`, `\B`:
			reads = true
		case "^", "$":
			reads = reads || multiline
		}
	})
	return reads
}

// hasTopLevelAlternation reports whether body holds a `|` outside every group and class, so
// anchoring it needs a group around it.
func hasTopLevelAlternation(body string) bool {
	depth, found := 0, false
	forEachToken(body, func(_ int, tok string, inClass bool) {
		if inClass {
			return
		}
		switch tok {
		case "(":
			depth++
		case ")":
			depth--
		case "|":
			found = found || depth == 0
		}
	})
	return found
}

// GroupNames returns the names a JavaScript regex literal's named capture groups use, in
// source order, and nil when the literal does not compile.
func GroupNames(source string) []string {
	p, _ := Compile(source)
	if p == nil {
		return nil
	}
	names := make([]string, len(p.Groups))
	for i, g := range p.Groups {
		names[i] = g.Name
	}
	return names
}

// MatchString reports whether the pattern matches s.
func (p *Pattern) MatchString(s string) bool {
	return p.re.MatchString(p.input(s))
}

// Captures reports whether the pattern matches s and, when it does, the text each named
// group captured, keyed by the group's name. A group that took no part in the match has no
// entry.
func (p *Pattern) Captures(s string) (map[string]string, bool) {
	in := p.input(s)
	match := p.re.FindStringSubmatchIndex(in)
	if match == nil {
		return nil, false
	}
	out := map[string]string{}
	for _, g := range p.Groups {
		for _, idx := range g.Indices {
			if match[2*idx] >= 0 {
				out[g.Name] = p.output(in[match[2*idx]:match[2*idx+1]])
				break
			}
		}
	}
	return out, true
}

// input returns s in the form the compiled pattern matches. A pattern compiled with the `u`
// flag matches code points, which a Go string already holds, so s comes back unchanged.
// Without the flag the pattern matches code units, so each code unit of s becomes one rune.
// A unit up to U+FFFF that is no surrogate stays the rune it is, and each surrogate becomes
// the rune unitRune maps it to.
//
// For example, "a😀" holds the code points U+0061 and U+1F600. In UTF-16 that is the units
// 0x0061, 0xD83D and 0xDE00, so input returns the three runes U+0061, U+F003D and U+F0600.
// A `.` in the pattern then matches one of the two surrogate runes, as it matches one code
// unit in JavaScript.
func (p *Pattern) input(s string) string {
	if !p.units {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		writeUnits(&b, r)
	}
	return b.String()
}

// output turns a span of the form input returns back into the string it stands for, so a
// captured group reads as the text it matched in s. It reverses input: each rune unitRune
// mapped a surrogate to becomes that surrogate unit again, and the units decode as UTF-16.
//
// For example, the runes U+F003D and U+F0600 become the units 0xD83D and 0xDE00, which
// decode to "😀". A span holding half a surrogate pair, as `(.)` captures from "😀", decodes
// to U+FFFD, since a Go string cannot hold a lone surrogate.
func (p *Pattern) output(s string) string {
	if !p.units {
		return s
	}
	units := make([]uint16, 0, len(s))
	for _, r := range s {
		if r >= unitBase && r < unitBase+0x800 {
			units = append(units, uint16(0xD800+r-unitBase))
			continue
		}
		units = append(units, utf16.Encode([]rune{r})...)
	}
	return string(utf16.Decode(units))
}

// writeUnits writes r as the runes that stand for its UTF-16 code units.
func writeUnits(b *strings.Builder, r rune) {
	if r <= 0xFFFF {
		b.WriteRune(unitRune(uint16(r)))
		return
	}
	hi, lo := utf16.EncodeRune(r)
	b.WriteRune(unitRune(uint16(hi)))
	b.WriteRune(unitRune(uint16(lo)))
}

// unitRune returns the rune that stands for one UTF-16 code unit. A surrogate maps into the
// private-use range at unitBase, and every other unit is its own code point.
func unitRune(u uint16) rune {
	if u >= 0xD800 && u <= 0xDFFF {
		return unitBase + rune(u-0xD800)
	}
	return rune(u)
}

// groupsOf returns the named groups of re, whose source is pattern, grouped by name in source
// order.
func groupsOf(re *regexp.Regexp, pattern string) []Group {
	optional := optionalCaptures(pattern)
	var groups []Group
	byName := map[string]int{}
	for idx, name := range re.SubexpNames() {
		if name == "" {
			continue
		}
		i, ok := byName[name]
		if !ok {
			i = len(groups)
			byName[name] = i
			groups = append(groups, Group{Name: name, Optional: true})
		}
		groups[i].Indices = append(groups[i].Indices, idx)
		// A name is left out only when every group using it can be. Two groups sharing a name
		// sit in different alternatives, so each is optional on its own.
		if len(groups[i].Indices) == 1 {
			groups[i].Optional = optional.Contains(idx)
		} else {
			groups[i].Optional = groups[i].Optional && optional.Contains(idx)
		}
	}
	return groups
}

// optionalCaptures returns the subexpression indices of the capture groups a match can leave
// out: one under a `?`, a `*`, a `{0,n}` repeat, or one side of an alternation.
func optionalCaptures(pattern string) set.Set[int] {
	out := set.NewSet[int]()
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return out
	}
	var walk func(re *syntax.Regexp, optional bool)
	walk = func(re *syntax.Regexp, optional bool) {
		switch re.Op {
		case syntax.OpCapture:
			if optional {
				out.Add(re.Cap)
			}
		case syntax.OpAlternate, syntax.OpStar, syntax.OpQuest:
			optional = true
		case syntax.OpRepeat:
			optional = optional || re.Min == 0
		}
		for _, sub := range re.Sub {
			walk(sub, optional)
		}
	}
	walk(re, false)
	return out
}

// rewriter rewrites the body of a JavaScript regex into Go syntax. dotAll reports whether the
// `s` flag is set, and unicode whether the `u` flag is.
type rewriter struct {
	dotAll, unicode bool
}

// body rewrites body, or returns why it cannot. It reads one token at a time, so an escaped
// `\(` and a `(` inside a character class are never taken for a group.
func (r rewriter) body(body string) (string, string) {
	var out strings.Builder
	inClass := false
	for i := 0; i < len(body); {
		ch := body[i]
		switch {
		case ch == '\\' && i+1 < len(body):
			escape, width, reason := r.escape(body[i:], inClass)
			if reason != "" {
				return "", reason
			}
			out.WriteString(escape)
			i += width
			continue
		case ch >= utf8.RuneSelf:
			cp, size := utf8.DecodeRuneInString(body[i:])
			out.WriteString(r.literal(cp))
			i += size
			continue
		case inClass:
			if ch == ']' {
				inClass = false
			}
			out.WriteByte(ch)
		case strings.HasPrefix(body[i:], "[]"):
			out.WriteString(`[^\x{0}-\x{10ffff}]`)
			i += 2
			continue
		case strings.HasPrefix(body[i:], "[^]"):
			out.WriteString(`[\x{0}-\x{10ffff}]`)
			i += 3
			continue
		case ch == '[':
			inClass = true
			out.WriteByte(ch)
		case ch == '.' && !r.dotAll:
			out.WriteString(dotClass)
		case strings.HasPrefix(body[i:], "(?<=") || strings.HasPrefix(body[i:], "(?<!"):
			return "", "uses lookbehind, which RE2 does not support"
		case strings.HasPrefix(body[i:], "(?=") || strings.HasPrefix(body[i:], "(?!"):
			return "", "uses lookahead, which RE2 does not support"
		case strings.HasPrefix(body[i:], "(?<"):
			out.WriteString("(?P<")
			i += len("(?<")
			continue
		default:
			out.WriteByte(ch)
		}
		i++
	}
	return out.String(), ""
}

// literal returns the Go spelling of the character cp written literally in the pattern.
// Matching code units splits an astral character into its two units.
func (r rewriter) literal(cp rune) string {
	if r.unicode || cp <= 0xFFFF {
		return string(cp)
	}
	var b strings.Builder
	hi, lo := utf16.EncodeRune(cp)
	b.WriteString(r.codePoint(hi))
	b.WriteString(r.codePoint(lo))
	return b.String()
}

// codePoint returns the Go escape for the character cp, mapping a surrogate unit into the
// range unitRune uses when the pattern matches code units.
func (r rewriter) codePoint(cp rune) string {
	if !r.unicode && cp >= 0xD800 && cp <= 0xDFFF {
		cp = unitRune(uint16(cp))
	}
	return fmt.Sprintf(`\x{%x}`, cp)
}

// escape rewrites the escape at the start of rest, which begins with a backslash. It returns
// the Go spelling, how many bytes of rest the escape spans, and a reason when the escape
// cannot be written in RE2. inClass reports whether the escape sits inside a character class.
func (r rewriter) escape(rest string, inClass bool) (string, int, string) {
	next := rest[1]
	switch {
	case next >= '1' && next <= '9' && !inClass:
		return "", 0, "uses a backreference, which RE2 does not support"
	case next == 'k' && strings.HasPrefix(rest[2:], "<") && !inClass:
		return "", 0, "uses a named backreference, which RE2 does not support"
	case next == 's':
		if inClass {
			return spaceSet, 2, ""
		}
		return "[" + spaceSet + "]", 2, ""
	case next == 'S':
		if inClass {
			return "", 0, "uses `\\S` inside a character class, which has no RE2 form"
		}
		return "[^" + spaceSet + "]", 2, ""
	case next == 'b' && inClass:
		return `\x{8}`, 2, ""
	case strings.IndexByte("dDwWtnvfr", next) >= 0 || (strings.IndexByte("bB", next) >= 0 && !inClass):
		return rest[:2], 2, ""
	case next == 'u' && r.unicode && strings.HasPrefix(rest[2:], "{"):
		end := strings.IndexByte(rest, '}')
		if end < 0 || !isHex(rest[3:end]) {
			return "", 0, "uses a malformed `\\u{` escape"
		}
		return `\x{` + rest[3:end] + `}`, end + 1, ""
	case next == 'u' && len(rest) >= 6 && isHex(rest[2:6]):
		return r.unicodeEscape(rest)
	case next == 'x' && len(rest) >= 4 && isHex(rest[2:4]):
		return `\x{` + rest[2:4] + `}`, 4, ""
	case next == 'c' && len(rest) >= 3 && isASCIILetter(rest[2]):
		return fmt.Sprintf(`\x{%x}`, rest[2]%32), 3, ""
	case next == '0' && (len(rest) < 3 || rest[2] < '0' || rest[2] > '9'):
		return `\x{0}`, 2, ""
	case (next == 'p' || next == 'P') && r.unicode:
		return propertyEscape(rest)
	case next >= utf8.RuneSelf:
		cp, size := utf8.DecodeRuneInString(rest[1:])
		return r.literal(cp), 1 + size, ""
	case isASCIILetter(next) || (next >= '0' && next <= '9'):
		// An escape JavaScript gives no meaning is the character it escapes, and the `u` flag
		// makes it a syntax error instead.
		if r.unicode {
			return "", 0, "uses the escape `\\" + string(next) + "`, which the `u` flag does not allow"
		}
		return string(next), 2, ""
	default:
		// An escaped punctuation character is itself in both languages.
		return rest[:2], 2, ""
	}
}

// unicodeEscape rewrites a `\uXXXX` escape at the start of rest. Under the `u` flag a high
// surrogate followed by a low one is the one character the pair encodes.
func (r rewriter) unicodeEscape(rest string) (string, int, string) {
	var unit uint32
	fmt.Sscanf(rest[2:6], "%x", &unit)
	if r.unicode && unit >= 0xD800 && unit <= 0xDBFF && len(rest) >= 12 && rest[6:8] == `\u` && isHex(rest[8:12]) {
		var low uint32
		fmt.Sscanf(rest[8:12], "%x", &low)
		if low >= 0xDC00 && low <= 0xDFFF {
			return r.codePoint(utf16.DecodeRune(rune(unit), rune(low))), 12, ""
		}
	}
	return r.codePoint(rune(unit)), 6, ""
}

// propertyEscape rewrites a `\p{…}` or `\P{…}` escape at the start of rest. JavaScript writes
// a script as `Script=Greek` or `sc=Greek` and a category as `General_Category=L` or `gc=L`,
// where Go writes the bare name.
func propertyEscape(rest string) (string, int, string) {
	if !strings.HasPrefix(rest[2:], "{") {
		return "", 0, "uses a malformed property escape"
	}
	end := strings.IndexByte(rest, '}')
	if end < 0 {
		return "", 0, "uses a malformed property escape"
	}
	name := rest[3:end]
	if key, value, ok := strings.Cut(name, "="); ok {
		switch key {
		case "Script", "sc", "General_Category", "gc":
			name = value
		default:
			return "", 0, "uses the property `" + key + "`, which RE2 does not support"
		}
	}
	return `\` + rest[1:2] + `{` + name + `}`, end + 1, ""
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
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
