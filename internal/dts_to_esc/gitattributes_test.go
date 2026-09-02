package dts_to_esc

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/stretchr/testify/require"
)

// escDataDir is the stdlib data tree, relative to the repository root.
const escDataDir = "internal/interop/data"

// generatedRule is one `.gitattributes` line's effect on
// `linguist-generated`. Set records whether the line turns the attribute
// on or off, so a `-linguist-generated` carve-out is read as a rule
// rather than skipped.
type generatedRule struct {
	Pattern string
	Set     bool
}

// generatedRules returns the `.gitattributes` lines that set or unset
// `linguist-generated`, in file order. Every pattern in the file
// contains a slash, so git anchors it at the repository root and
// path.Match reproduces the match against a root-relative path.
func generatedRules(t *testing.T, repoRoot string) []generatedRule {
	t.Helper()

	contents, err := os.ReadFile(filepath.Join(repoRoot, ".gitattributes"))
	require.NoError(t, err)

	var rules []generatedRule
	for _, line := range strings.Split(string(contents), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		pattern, attrs := fields[0], fields[1:]
		require.NotContains(t, pattern, "**",
			"isGenerated matches %q with path.Match, which has no ** operator",
			pattern)

		for _, attr := range attrs {
			switch attr {
			case "linguist-generated", "linguist-generated=true":
				rules = append(rules, generatedRule{Pattern: pattern, Set: true})
			case "-linguist-generated", "linguist-generated=false":
				rules = append(rules, generatedRule{Pattern: pattern, Set: false})
			}
		}
	}
	require.NotEmpty(t, rules)
	return rules
}

// isGenerated reports the `linguist-generated` value git resolves for a
// repository-root-relative path. A later line wins over an earlier one,
// so the walk keeps the last match instead of stopping at the first.
func isGenerated(t *testing.T, rules []generatedRule, file string) bool {
	t.Helper()

	generated := false
	for _, rule := range rules {
		matched, err := path.Match(rule.Pattern, file)
		require.NoError(t, err)
		if matched {
			generated = rule.Set
		}
	}
	return generated
}

// generatedPackageFiles returns the root-relative path of every `.esc`
// package a converter run writes. Those are the ones the partition table
// routes to, plus `web:dom`, which every unmapped name from a
// DOMResidualSources file routes to.
func generatedPackageFiles() set.Set[string] {
	files := set.NewSet[string]()
	for _, pkg := range Partition {
		files.Add(path.Join(escDataDir, pkg.File))
	}
	files.Add(path.Join(escDataDir, WebDOM.File))
	return files
}

// committedEscFiles returns the root-relative path of every `.esc` file
// under the stdlib data tree, sorted.
func committedEscFiles(t *testing.T, repoRoot string) []string {
	t.Helper()

	var files []string
	root := filepath.Join(repoRoot, escDataDir)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(p) != ".esc" {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, path.Join(escDataDir, filepath.ToSlash(rel)))
		return nil
	})
	require.NoError(t, err)

	sort.Strings(files)
	return files
}

// A converter run writes every routed package from scratch, so each one
// has to reach GitHub marked generated for a regenerated tree to collapse
// in a pull request diff.
func TestGitattributes_MarksEveryGeneratedPackage(t *testing.T) {
	repoRoot, err := findRepoRoot()
	require.NoError(t, err)
	rules := generatedRules(t, repoRoot)

	files := generatedPackageFiles().ToSlice()
	sort.Strings(files)
	for _, file := range files {
		require.True(t, isGenerated(t, rules, file),
			"%s is a generated package but .gitattributes leaves it unmarked",
			file)
	}
}

// The patterns cover whole directories, which stays correct only while
// every `.esc` file under them is a converter output. A hand-authored
// package needs a carve-out, per the list of packages the generator never
// writes in planning/builtins/implementation_plan.md §6.4.
func TestGitattributes_MarksOnlyGeneratedPackages(t *testing.T) {
	repoRoot, err := findRepoRoot()
	require.NoError(t, err)
	rules := generatedRules(t, repoRoot)
	generated := generatedPackageFiles()

	for _, file := range committedEscFiles(t, repoRoot) {
		if !isGenerated(t, rules, file) {
			continue
		}
		require.True(t, generated.Contains(file),
			"%s is marked linguist-generated but the partition table routes "+
				"no package to it; scope the .gitattributes patterns to the "+
				"generated packages", file)
	}
}
