package resolver

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// exports.go reads the package.json fields that map an import's subpath to a file:
// `exports` and `typesVersions`. Both are JSON objects whose first matching key wins,
// so they are decoded with their keys kept in source order.

// typeScriptVersion is the TypeScript version that a `types@<range>` condition and a
// `typesVersions` range are matched against. A package that ships older declarations
// for older compilers is read through its newest ones.
var typeScriptVersion = []int{5, 9, 0}

// jsonKind is the kind of value a jsonNode holds.
type jsonKind int

const (
	jsonOther jsonKind = iota
	jsonNull
	jsonString
	jsonArray
	jsonObject
)

// jsonNode is a decoded JSON value whose object keys keep their source order. A
// number or a boolean decodes as jsonOther, since neither names a file.
type jsonNode struct {
	kind jsonKind
	// str is the value of a jsonString.
	str string
	// items are the elements of a jsonArray.
	items []*jsonNode
	// keys and values are the members of a jsonObject, in source order.
	keys   []string
	values []*jsonNode
}

// decodeOrdered decodes data into a jsonNode.
func decodeOrdered(data []byte) (*jsonNode, error) {
	return decodeNode(json.NewDecoder(bytes.NewReader(data)))
}

// decodeNode decodes the next value dec holds.
func decodeNode(dec *json.Decoder) (*jsonNode, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case nil:
		return &jsonNode{kind: jsonNull}, nil
	case string:
		return &jsonNode{kind: jsonString, str: t}, nil
	case json.Delim:
		node := &jsonNode{kind: jsonArray}
		if t == '{' {
			node.kind = jsonObject
		}
		for dec.More() {
			if node.kind == jsonObject {
				keyTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, _ := keyTok.(string)
				node.keys = append(node.keys, key)
			}
			value, err := decodeNode(dec)
			if err != nil {
				return nil, err
			}
			if node.kind == jsonObject {
				node.values = append(node.values, value)
			} else {
				node.items = append(node.items, value)
			}
		}
		// The closing delimiter.
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return node, nil
	}
	return &jsonNode{kind: jsonOther}, nil
}

// exportsEntry returns the value an `exports` field maps subpath to, and the text a
// `*` in that value stands for. subpath is `.` for the package itself, or a path such
// as `./utils`. listed is false when the field maps no key to subpath.
//
// A field that is a string, an array, or an object with no key starting with `.` is
// the entry for `.` alone. Otherwise a key equal to subpath wins. Failing that, a key
// holding one `*` matches when subpath starts with the text before the `*`, ends
// with the text after it, and leaves at least one character for the `*`. The key
// with the longest text before the `*` wins, and between two of equal length the
// longer key wins.
func exportsEntry(exports *jsonNode, subpath string) (value *jsonNode, star string, listed bool) {
	if !hasSubpathKeys(exports) {
		return exports, "", subpath == "."
	}
	if i, star, found := patternMatch(exports.keys, subpath); found {
		return exports.values[i], star, true
	}
	return nil, "", false
}

// hasSubpathKeys reports whether node is an object with a key that starts with `.`.
func hasSubpathKeys(node *jsonNode) bool {
	if node.kind != jsonObject {
		return false
	}
	for _, key := range node.keys {
		if strings.HasPrefix(key, ".") {
			return true
		}
	}
	return false
}

// patternMatch returns the index of the key among keys that subpath matches, and the
// text a `*` in that key stands for, by the rules exportsEntry lists. found is false
// when no key matches.
func patternMatch(keys []string, subpath string) (index int, star string, found bool) {
	best, bestPrefix, bestLen := -1, -1, -1
	for i, key := range keys {
		if key == subpath {
			return i, "", true
		}
		prefix, suffix, hasStar := strings.Cut(key, "*")
		if !hasStar || strings.Contains(suffix, "*") {
			continue
		}
		if len(subpath) <= len(prefix)+len(suffix) ||
			!strings.HasPrefix(subpath, prefix) || !strings.HasSuffix(subpath, suffix) {
			continue
		}
		if len(prefix) > bestPrefix || (len(prefix) == bestPrefix && len(key) > bestLen) {
			best, bestPrefix, bestLen = i, len(prefix), len(key)
			star = subpath[len(prefix) : len(subpath)-len(suffix)]
		}
	}
	return best, star, best >= 0
}

// exportsTarget returns the declaration file an `exports` value leads to inside the
// package at pkgDir, with star replacing each `*` in a target. A target names a file
// relative to pkgDir. It starts with `./` and has no `..` or `node_modules`
// segment, so it stays inside the package. The value is read this way:
//
//   - A string is a target. A `.d.ts` target is that file, and a `.js` target leads
//     to the `.d.ts` file beside it.
//   - An array tries each element in turn.
//   - An object tries, in source order, the value of each key activeCondition
//     accepts.
//   - `null` leads nowhere.
//
// path is "" when no target leads to a file that exists. missing is then the first
// `.d.ts` target that names a file which does not exist, or "" when there is none.
func exportsTarget(pkgDir string, value *jsonNode, star string) (path, missing string) {
	switch value.kind {
	case jsonString:
		target := strings.ReplaceAll(value.str, "*", star)
		rel, relative := strings.CutPrefix(target, "./")
		if !relative || leavesPackage(rel) {
			return "", ""
		}
		if path := firstDeclarationFile(pkgDir, rel); path != "" {
			return path, ""
		}
		if isDeclarationFile(rel) {
			return "", joinRel(pkgDir, rel)
		}
	case jsonArray:
		for _, item := range value.items {
			path, itemMissing := exportsTarget(pkgDir, item, star)
			if path != "" {
				return path, ""
			}
			if missing == "" {
				missing = itemMissing
			}
		}
	case jsonObject:
		for i, key := range value.keys {
			if !activeCondition(key) {
				continue
			}
			path, valueMissing := exportsTarget(pkgDir, value.values[i], star)
			if path != "" {
				return path, ""
			}
			if missing == "" {
				missing = valueMissing
			}
		}
	}
	return "", missing
}

// leavesPackage reports whether rel has a `..` or `node_modules` segment.
func leavesPackage(rel string) bool {
	for _, segment := range strings.Split(rel, "/") {
		if segment == ".." || segment == "node_modules" {
			return true
		}
	}
	return false
}

// activeCondition reports whether an `exports` condition applies when reading types.
// The conditions are `types`, `import`, `require`, `node`, `default`, and a
// `types@<range>` whose range typeScriptVersion falls in.
func activeCondition(key string) bool {
	switch key {
	case "types", "import", "require", "node", "default":
		return true
	}
	versions, versioned := strings.CutPrefix(key, "types@")
	return versioned && matchesVersionRange(versions)
}

// typesVersionsPaths returns the paths a `typesVersions` field redirects rel to, in
// the order they are tried. rel is a path inside the package with no leading `./`,
// such as `index.d.ts` or `utils`. Only the first range typeScriptVersion falls in is
// read, and its mapping is matched the way exportsEntry matches subpath keys. It
// returns nil when no range applies or the mapping has no key rel matches.
func typesVersionsPaths(typesVersions *jsonNode, rel string) []string {
	if typesVersions == nil || typesVersions.kind != jsonObject {
		return nil
	}
	for i, versions := range typesVersions.keys {
		if !matchesVersionRange(versions) {
			continue
		}
		mapping := typesVersions.values[i]
		if mapping.kind != jsonObject {
			return nil
		}
		j, star, found := patternMatch(mapping.keys, rel)
		if !found {
			return nil
		}
		var paths []string
		for _, target := range mapping.values[j].items {
			if target.kind == jsonString {
				paths = append(paths, strings.ReplaceAll(target.str, "*", star))
			}
		}
		return paths
	}
	return nil
}

// matchesVersionRange reports whether typeScriptVersion falls in versions, a range
// such as `>=4.1`, `<=5.0` or `>=4.1 <5`. Every space-separated comparator has to
// hold, and `*` holds for every version. A comparator compares only the components
// its version spells, so `<=5.0` holds for 5.0.3 and `>5` holds only from 6.0.0.
// A comparator that does not parse never holds.
func matchesVersionRange(versions string) bool {
	if strings.TrimSpace(versions) == "*" {
		return true
	}
	comparators := strings.Fields(versions)
	if len(comparators) == 0 {
		return false
	}
	for _, comparator := range comparators {
		op := strings.TrimRight(comparator[:min(2, len(comparator))], "0123456789.")
		version, ok := parseVersion(comparator[len(op):])
		if !ok {
			return false
		}
		order := compareVersionPrefix(typeScriptVersion, version)
		var holds bool
		switch op {
		case ">=":
			holds = order >= 0
		case ">":
			holds = order > 0
		case "<=":
			holds = order <= 0
		case "<":
			holds = order < 0
		case "=", "":
			holds = order == 0
		}
		if !holds {
			return false
		}
	}
	return true
}

// parseVersion returns the components of a version such as `5`, `5.0` or `5.0.2`.
func parseVersion(text string) ([]int, bool) {
	parts := strings.Split(text, ".")
	if len(parts) > 3 {
		return nil, false
	}
	version := make([]int, len(parts))
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return nil, false
		}
		version[i] = n
	}
	return version, true
}

// compareVersionPrefix compares the first len(prefix) components of version with
// prefix, returning -1, 0 or 1.
func compareVersionPrefix(version, prefix []int) int {
	for i, want := range prefix {
		switch {
		case version[i] < want:
			return -1
		case version[i] > want:
			return 1
		}
	}
	return 0
}

// isDeclarationFile reports whether rel names a declaration file.
func isDeclarationFile(rel string) bool {
	return strings.HasSuffix(rel, ".d.ts") || strings.HasSuffix(rel, ".d.mts") || strings.HasSuffix(rel, ".d.cts")
}

// declarationCandidates returns the files that may hold the types for rel, a path
// inside a package, in the order they are tried. A declaration file is itself. A
// JavaScript file's types are the declaration file beside it, so `index.js` gives
// `index.d.ts` and `index.mjs` gives `index.d.mts`. Any other path gives itself with
// `.d.ts` added, then the `index.d.ts` inside it.
func declarationCandidates(rel string) []string {
	if isDeclarationFile(rel) {
		return []string{rel}
	}
	for _, ext := range []struct{ js, dts string }{{".mjs", ".d.mts"}, {".cjs", ".d.cts"}, {".js", ".d.ts"}} {
		if base, found := strings.CutSuffix(rel, ext.js); found {
			return []string{base + ext.dts}
		}
	}
	return []string{rel + ".d.ts", rel + "/index.d.ts"}
}

// firstDeclarationFile returns the first of rel's declarationCandidates that names a
// file inside pkgDir, or "" when none does.
func firstDeclarationFile(pkgDir, rel string) string {
	for _, candidate := range declarationCandidates(rel) {
		if path := joinRel(pkgDir, candidate); isFile(path) {
			return path
		}
	}
	return ""
}
