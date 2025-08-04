package checker

import (
	. "github.com/escalier-lang/escalier/internal/type_system"
)

// findNamedGroups extracts the names of any named capture groups from RegexTypes that appear in the given type.
// Named capture groups in regex have the syntax (?P<name>pattern) or (?<name>pattern).
func (c *Checker) findNamedGroups(t Type) map[string]Type {
	visitor := &NamedCaptureGroupExtractor{
		checker:     c,
		namedGroups: make(map[string]Type), // Map capture group names to fresh type vars
	}
	t.Accept(visitor)

	return visitor.namedGroups
}

// NamedCaptureGroupExtractor extracts named capture groups from regex literals in types
type NamedCaptureGroupExtractor struct {
	checker     *Checker
	namedGroups map[string]Type
}

func (v *NamedCaptureGroupExtractor) EnterType(t Type) Type {
	// No-op - just for traversal
	return nil
}

func (v *NamedCaptureGroupExtractor) ExitType(t Type) Type {
	t = Prune(t)

	if regexType, ok := t.(*RegexType); ok {
		// Create a new RegexType with fresh type variables for named capture groups
		newGroups := make(map[string]Type)
		for name := range regexType.Groups {
			if name != "" {
				freshVar := v.checker.FreshVar()
				v.namedGroups[name] = freshVar
				newGroups[name] = freshVar
			}
		}

		// Return a new RegexType with the fresh type variables
		newRegexType := &RegexType{
			Regex:  regexType.Regex,
			Groups: newGroups,
		}
		newRegexType.SetProvenance(regexType.Provenance())
		return newRegexType
	}

	// For all other types, return nil to let Accept handle the traversal
	return nil
}

// replaceRegexGroupTypes replaces the named capture groups in RegexType instances
// with their corresponding types from the substitutions map.
func (c *Checker) replaceRegexGroupTypes(t Type, substitutions map[string]Type) Type {
	visitor := &RegexTypeReplacer{
		substitutions: substitutions,
	}
	return t.Accept(visitor)
}

// RegexTypeReplacer substitutes named capture groups in RegexType instances
// with their corresponding types from the substitutions map
type RegexTypeReplacer struct {
	substitutions map[string]Type
}

func (v *RegexTypeReplacer) EnterType(t Type) Type {
	// No-op - just for traversal
	return nil
}

func (v *RegexTypeReplacer) ExitType(t Type) Type {
	t = Prune(t)

	if regexType, ok := t.(*RegexType); ok {
		// Check if any named groups in this regex type have substitutions
		hasSubstitutions := false
		newGroups := make(map[string]Type)

		for groupName, groupType := range regexType.Groups {
			if substitutionType, exists := v.substitutions[groupName]; exists {
				// Use the substitution type
				newGroups[groupName] = substitutionType
				hasSubstitutions = true
			} else {
				// Keep the original type
				newGroups[groupName] = groupType
			}
		}

		// Only create a new RegexType if there were substitutions
		if hasSubstitutions {
			newRegexType := &RegexType{
				Regex:  regexType.Regex,
				Groups: newGroups,
			}
			newRegexType.SetProvenance(regexType.Provenance())
			return newRegexType
		}
	}

	// For all other types, return nil to let Accept handle the traversal
	return nil
}
