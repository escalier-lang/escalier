package interop

import (
	"fmt"
	"strings"
)

// ErrDuplicateMember reports that two override entries at the same
// OverrideTier targeted the same slot. Cross-tier shadowing is silent;
// within-tier duplication is a hard error.
type ErrDuplicateMember struct {
	Path          Path
	First, Second Origin
}

func (e *ErrDuplicateMember) Error() string {
	return fmt.Sprintf(
		"duplicate override entry for %s\n  first defined at %s:%d\n  redefined at %s:%d",
		pathString(e.Path),
		e.First.FilePath, e.First.Span.Start.Line,
		e.Second.FilePath, e.Second.Span.Start.Line,
	)
}

// ErrShapeConflict reports two declarations at the same Path with
// incompatible variants — e.g. one declares `Foo` as a namespace and
// the other as a class. Distinct from ErrDuplicateMember (which is for
// same-shape collisions within a tier) so callers can render a more
// specific diagnostic.
type ErrShapeConflict struct {
	Path          Path
	First, Second Origin
}

func (e *ErrShapeConflict) Error() string {
	return fmt.Sprintf(
		"shape conflict for %s\n  first defined at %s:%d\n  redefined at %s:%d",
		pathString(e.Path),
		e.First.FilePath, e.First.Span.Start.Line,
		e.Second.FilePath, e.Second.Span.Start.Line,
	)
}

// ErrUnknownMember reports an override entry whose target does not
// exist on the original declaration. `Available` is the list of
// sibling names on the original's matching MemberSet for did-you-mean
// suggestions.
type ErrUnknownMember struct {
	Path      Path
	Override  Origin
	Available []string
}

func (e *ErrUnknownMember) Error() string {
	msg := fmt.Sprintf(
		"override target %s not found on original declaration\n  override at %s:%d",
		pathString(e.Path),
		e.Override.FilePath, e.Override.Span.Start.Line,
	)
	if len(e.Available) > 0 {
		msg += "\n  available: " + strings.Join(e.Available, ", ")
	}
	return msg
}

// ErrSignatureMismatch reports that an override signature's shape
// differs from the original on a non-receiver axis (arity, parameter
// type, return type, or overload count).
type ErrSignatureMismatch struct {
	Path           Path
	Field          string // "arity" | "param[i]" | "return" | "overload count" | "overload[i]/<field>"
	Override       string // pretty-printed override side (or count, as string)
	Original       string // pretty-printed original side
	OverrideOrigin Origin
}

func (e *ErrSignatureMismatch) Error() string {
	return fmt.Sprintf(
		"override of %s changes signature shape (%s): override=%s, original=%s\n  override at %s:%d",
		pathString(e.Path), e.Field, e.Override, e.Original,
		e.OverrideOrigin.FilePath, e.OverrideOrigin.Span.Start.Line,
	)
}

// ErrPropertyTypeMismatch reports that a non-function leaf (a property,
// a free type-alias or value binding, etc.) has an override type that
// is not equivalent to the original under the property-consistency
// rule. The rule permits three directions of refinement:
//
//   - exact structural equality (Type.Equals)
//   - Mut-wrapping the original (override = Mut[orig])
//   - tightening a sloppy TS-side any/unknown to a concrete shape
//
// Anything else — brand narrowing, unrelated shapes, function-vs-value
// kind mismatch — surfaces as this error. The merge still installs the
// override side so downstream consumers see one consistent answer.
type ErrPropertyTypeMismatch struct {
	Path           Path
	Override       string
	Original       string
	OverrideOrigin Origin
}

func (e *ErrPropertyTypeMismatch) Error() string {
	return fmt.Sprintf(
		"override of %s changes property type incompatibly: override=%s, original=%s\n  override at %s:%d",
		pathString(e.Path), e.Override, e.Original,
		e.OverrideOrigin.FilePath, e.OverrideOrigin.Span.Start.Line,
	)
}

// ErrGenericArityMismatch reports that an override class/interface
// declares a different number of type parameters than the original.
type ErrGenericArityMismatch struct {
	Path     Path
	Override int
	Original int
}

func (e *ErrGenericArityMismatch) Error() string {
	return fmt.Sprintf(
		"override of %s declares %d type parameter(s); original has %d",
		pathString(e.Path), e.Override, e.Original,
	)
}

// pathString renders a Path for diagnostics. Module-scoped paths print
// as `module "name"::owner.member`; global paths drop the module prefix.
func pathString(p Path) string {
	var b strings.Builder
	if p.Module == "" && p.Owner == nil && p.Name == nil {
		return "<unknown>"
	}
	if p.Module != "" {
		b.WriteString(`module "`)
		b.WriteString(p.Module)
		b.WriteString(`"::`)
	}
	if p.Owner != nil {
		for i, seg := range qualIdentSegments(p.Owner) {
			if i > 0 {
				b.WriteString(".")
			}
			b.WriteString(seg)
		}
		switch p.Kind {
		case KindMethod, KindGetter, KindSetter, KindProperty:
			if p.Static {
				b.WriteString(".")
			} else {
				b.WriteString(".prototype.")
			}
		case KindCtor:
			b.WriteString(".constructor")
			return b.String()
		default:
			b.WriteString(".")
		}
	}
	if p.Name != nil {
		b.WriteString(canonicalNameFromPK(p.Name))
	}
	return b.String()
}
