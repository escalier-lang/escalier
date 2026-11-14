package main

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/escalier-lang/escalier/internal/solver"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// hoverParams is the params of `textDocument/hover` with Escalier's verbosity extension.
// The LSP has no verbosity field on a hover request, so a client that does not know the
// extension sends no level and gets level 0.
type hoverParams struct {
	protocol.HoverParams
	// VerbosityLevel is how many layers of aliases the hover expands. Each layer also
	// evaluates indexed access, `keyof`, conditional and mapped types. A negative level
	// reads as 0.
	VerbosityLevel int `json:"verbosityLevel,omitempty"`
}

// verboseHover is a hover with Escalier's verbosity extension. The level fields let a
// client offer an expand or collapse action, and a client that does not know the
// extension reads it as a plain hover.
type verboseHover struct {
	Contents protocol.MarkupContent `json:"contents"`
	// VerbosityLevel is the level the hover was rendered at. It is lower than the level
	// asked for when expanding stops changing the type before that level.
	VerbosityLevel int `json:"verbosityLevel,omitempty"`
	// CanIncreaseVerbosity reports whether the next level renders differently.
	CanIncreaseVerbosity bool `json:"canIncreaseVerbosity,omitempty"`
	// CanDecreaseVerbosity reports whether VerbosityLevel is above 0.
	CanDecreaseVerbosity bool `json:"canDecreaseVerbosity,omitempty"`
}

// newVerboseHover returns a hover that shows code as an Escalier code block, with no
// level to expand to.
func newVerboseHover(code string) *verboseHover {
	return &verboseHover{Contents: codeHover(code).Contents.(protocol.MarkupContent)}
}

// aliasHover returns the hover at level for the cursor on a type alias's name or on a
// reference to a type alias, and false when q is on neither.
//
// Level 0 is the hover typeHover renders. Level n renders the alias's type with n layers
// of aliases expanded, each layer evaluated by Info.Evaluate. A declaration expands the
// type it writes, so `type Y = Point["y"]` renders at level 1 as `type Y = number`. A
// reference expands itself as the first layer, so `Box<string>` for
// `type Box<T> = {value: T}` renders at level 1 as `type Box<string> = {value: string}`.
func aliasHover(q hoverQuery, level int) (*verboseHover, bool) {
	if q.info == nil {
		return nil, false
	}
	switch n := q.node.(type) {
	case *ast.TypeDecl:
		if !n.Name.Span().Contains(q.loc) {
			return nil, false
		}
		written := q.info.TypeOf(n.TypeAnn)
		self, ok := q.info.TypeOf(n.Name).(*soltype.AliasType)
		if written == nil || !ok {
			return nil, false
		}
		params, _ := q.info.AliasTypeParams(self)
		e := aliasExpansion{info: q.info, params: params}
		header := "type " + n.Name.Name + typeParamsText(n.TypeParams)
		return e.hover(declHoverText(q.info, n), header, written, e.print(written), level), true
	case *ast.TypeRefTypeAnn:
		if !n.Name.Span().Contains(q.loc) {
			return nil, false
		}
		if ident, ok := n.Name.(*ast.Ident); ok {
			if _, isParam := typeParamInScope(q.ancestors, ident.Name, q.loc); isParam {
				return nil, false
			}
		}
		ref, ok := q.info.TypeOf(n).(*soltype.AliasType)
		if !ok {
			return nil, false
		}
		decl, ok := refDecl(q, n)
		if !ok {
			return nil, false
		}
		alias, ok := decl.(*ast.TypeDecl)
		if !ok {
			return nil, false
		}
		// A reference can name a type parameter of the alias declaration around it,
		// as `Box<T>` does in `type Wrap<T> = Box<T>`. Any other type variable, such as
		// a generic function's parameter, has no name to print under.
		params := enclosingAliasParams(q)
		if !onlyVarsOf(ref, params) {
			return nil, false
		}
		e := aliasExpansion{info: q.info, params: params}
		declInfo := q.infoFor(alias.Span().SourceID)
		// Level 0 shows the declaration, so level 1 exists only when it prints differently
		// from the declaration's own type. A prelude alias such as `Partial` is declared
		// in a file with no records, so its baseline stays empty.
		baseline := ""
		if declInfo != nil {
			if written := declInfo.TypeOf(alias.TypeAnn); written != nil {
				declParams, _ := declInfo.AliasTypeParams(ref)
				baseline = soltype.PrintWithParams(written, declParams)
			}
		}
		header := "type " + e.print(ref)
		return e.hover(refDeclText(q, alias), header, ref, baseline, level), true
	}
	return nil, false
}

// enclosingAliasParams returns the type parameters of the type alias declaration that
// encloses the cursor, or nil when none does.
func enclosingAliasParams(q hoverQuery) []*soltype.TypeParam {
	for i := len(q.ancestors) - 1; i >= 0; i-- {
		decl, ok := q.ancestors[i].(*ast.TypeDecl)
		if !ok {
			continue
		}
		self, ok := q.info.TypeOf(decl.Name).(*soltype.AliasType)
		if !ok {
			return nil
		}
		params, _ := q.info.AliasTypeParams(self)
		return params
	}
	return nil
}

// onlyVarsOf reports whether every type variable in t is the variable of one of params.
func onlyVarsOf(t soltype.Type, params []*soltype.TypeParam) bool {
	named := set.NewSet[*soltype.TypeVarType]()
	for _, p := range params {
		named.Add(p.Var)
	}
	finder := &typeVarFinder{named: named}
	t.Accept(finder, soltype.Positive)
	return !finder.foundOther
}

// typeVarFinder is a walking visitor that records whether it reached a type variable
// outside named.
type typeVarFinder struct {
	named      set.Set[*soltype.TypeVarType]
	foundOther bool
}

func (v *typeVarFinder) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	if tv, ok := t.(*soltype.TypeVarType); ok && !v.named.Contains(tv) {
		v.foundOther = true
	}
	return soltype.EnterResult{}
}

func (v *typeVarFinder) ExitType(t soltype.Type, pol soltype.Polarity) soltype.Type {
	return t
}

// aliasExpansion expands and prints an alias's type one layer at a time, reading the
// registries of the run that recorded info. params names the type variables the type
// can hold.
type aliasExpansion struct {
	info   *solver.Info
	params []*soltype.TypeParam
}

// print renders t with each variable of params under its source name.
func (e aliasExpansion) print(t soltype.Type) string {
	return soltype.PrintWithParams(t, e.params)
}

// hover renders t at level under header, as `header = T`, and renders level0 at level 0.
// baseline is the printed type that level 0 shows. Level 1 exists only when it prints
// differently from baseline. A level past the point where expanding stops changing t
// renders at that point.
func (e aliasExpansion) hover(level0, header string, t soltype.Type, baseline string, level int) *verboseHover {
	cur, printed := t, baseline
	reached := 0
	canIncrease := false
	for {
		next := e.expandLayer(cur)
		nextPrinted := e.print(next)
		canIncrease = nextPrinted != printed
		if !canIncrease || reached >= level {
			break
		}
		cur, printed = next, nextPrinted
		reached++
	}
	text := level0
	if reached > 0 {
		text = header + " = " + printed
	}
	h := newVerboseHover(text)
	h.VerbosityLevel = reached
	h.CanIncreaseVerbosity = canIncrease
	h.CanDecreaseVerbosity = reached > 0
	return h
}

// expandLayer returns t with each alias reference and `typeof` query in it expanded one
// step and then evaluated by Info.Evaluate. An alias the expansion uncovers stays a
// reference until the next layer.
func (e aliasExpansion) expandLayer(t soltype.Type) soltype.Type {
	return e.info.Evaluate(t.Accept(&aliasLayerExpander{info: e.info}, soltype.Positive))
}

// aliasLayerExpander is a rewriting visitor that replaces an alias reference by its body
// and a `typeof` query by the type it reads. It skips each replacement's children, so a
// walk expands one step deep.
type aliasLayerExpander struct {
	info *solver.Info
}

func (v *aliasLayerExpander) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	switch t := t.(type) {
	case *soltype.AliasType:
		if body, ok := v.info.ExpandAlias(t); ok {
			return soltype.EnterResult{Type: body, SkipChildren: true}
		}
	case *soltype.TypeofType:
		return soltype.EnterResult{Type: t.Ty, SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (v *aliasLayerExpander) ExitType(t soltype.Type, pol soltype.Polarity) soltype.Type {
	return t
}
