package main

import (
	"strings"
	"unicode/utf16"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/compiler"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/escalier-lang/escalier/internal/solver"
)

// textDocumentSignatureHelp returns the signatures of the call whose argument list holds
// the cursor, or nil when the cursor is in no argument list or the callee has no
// signature. A function has one signature per overload, and a class has one per
// constructor, since calling a class constructs it.
func (s *Server) textDocumentSignatureHelp(context *glsp.Context, params *protocol.SignatureHelpParams) (*protocol.SignatureHelp, error) {
	uri := params.TextDocument.URI

	s.mu.RLock()
	// The client sends this request right after typing `(` or `,`, so the edit that
	// typed it is usually still being checked.
	s.waitForFreshCheck(uri)
	var call *ast.CallExpr
	var active int
	var info *solver.Info
	var scope *solver.Scope
	if co := s.checkOutput; co != nil {
		sourceID := s.sourceIDForURI(uri)
		if src := co.SourceByID(sourceID); src != nil {
			// The cursor is converted against the text the AST was parsed from, so the
			// offset indexes the same revision the spans do.
			loc := posToLoc(lineMapFor(src), params.Position)
			call, active = findCallAtLocation(co, sourceID, src.Contents, loc)
		}
		info = co.InfoFor(sourceID)
		scope = topLevelScope(co, sourceID)
	}
	s.mu.RUnlock()
	if call == nil || info == nil {
		return nil, nil
	}

	s.solverMu.RLock()
	sigs := calleeSignatures(info, scope, call.Callee)
	s.solverMu.RUnlock()
	if len(sigs) == 0 {
		return nil, nil
	}
	return signatureHelp(calleeName(call.Callee), sigs, active), nil
}

// topLevelScope returns the outermost scope the package declares names in for the file
// sourceID names. That is a bin/ script's own scope or a lib/ file's import scope, and
// nil when co holds neither.
func topLevelScope(co *compiler.CheckOutput, sourceID int) *solver.Scope {
	if scope, ok := co.ScriptScopes[sourceID]; ok {
		return scope
	}
	return co.FileScopes[sourceID]
}

// callFinder records the innermost call whose argument list holds cursor. text is the
// file the AST was parsed from.
type callFinder struct {
	ast.DefaultVisitor
	cursor ast.Location
	text   string
	call   *ast.CallExpr
	// open is the offset of call's `(`.
	open int
}

func (v *callFinder) EnterExpr(e ast.Expr) bool {
	if !e.Span().Contains(v.cursor) {
		return false
	}
	if call, ok := e.(*ast.CallExpr); ok {
		if open, ok := argListOpen(call, v.text); ok && inArgList(call, v.text, open, v.cursor) {
			// A call nested in an argument is entered after the call holding it, so the
			// innermost call is the last one recorded.
			v.call = call
			v.open = open
		}
	}
	return true
}

// findCallAtLocation returns the innermost call in the file sourceID names whose argument
// list holds loc, and the index of the argument loc is in. text is the file's contents.
// The call is nil when no argument list holds loc.
func findCallAtLocation(co *compiler.CheckOutput, sourceID int, text string, loc ast.Location) (*ast.CallExpr, int) {
	v := &callFinder{cursor: loc, text: text}
	if script, ok := co.Scripts[sourceID]; ok {
		script.Accept(v)
	} else if co.Module != nil {
		walkModuleFile(v, co.Module, sourceID)
	}
	if v.call == nil {
		return nil, 0
	}
	return v.call, argIndex(v.call, text, v.open, loc)
}

// argListOpen returns the offset of the `(` that opens call's argument list. Only
// whitespace and the `?` of an optional call `f?(x)` may sit between the callee and it.
func argListOpen(call *ast.CallExpr, text string) (int, bool) {
	for i := call.Callee.Span().End.Offset; i < len(text); i++ {
		switch text[i] {
		case '(':
			return i, true
		case ' ', '\t', '\r', '\n', '?':
		default:
			return 0, false
		}
	}
	return 0, false
}

// inArgList reports whether loc lies between the parentheses of call, whose `(` is at
// open. A call missing its `)` reaches to the end of its span, which is where the parser
// stopped reading arguments.
func inArgList(call *ast.CallExpr, text string, open int, loc ast.Location) bool {
	if loc.Offset <= open {
		return false
	}
	end := call.Span().End.Offset
	if end > open+1 && end <= len(text) && text[end-1] == ')' {
		return loc.Offset < end
	}
	return loc.Offset <= end
}

// argIndex returns the index of the argument of call that loc is in, counting the commas
// between the `(` at open and loc. Only the gaps between arguments are searched, so a
// comma inside an argument such as `f(g(a, b), |)` does not count.
func argIndex(call *ast.CallExpr, text string, open int, loc ast.Location) int {
	index := 0
	gapStart := open + 1
	for _, arg := range call.Args {
		if comma := strings.IndexByte(text[gapStart:arg.Span().Start.Offset], ','); comma >= 0 &&
			gapStart+comma < loc.Offset {
			index++
		}
		gapStart = arg.Span().End.Offset
	}
	if gapStart < loc.Offset && loc.Offset <= len(text) {
		if strings.Contains(text[gapStart:loc.Offset], ",") {
			index++
		}
	}
	return index
}

// calleeSignatures returns the signatures a call to callee may resolve to, in the order
// they are declared. info is the table callee was checked with and scope is the top-level
// scope of the file callee is in.
func calleeSignatures(info *solver.Info, scope *solver.Scope, callee ast.Expr) []*soltype.FuncType {
	switch callee := callee.(type) {
	case *ast.IdentExpr:
		// The call site records only the overload arm the call resolved to, and a generic
		// callee instantiated for the call. The binding holds every arm, each with its type
		// parameters, so it is read when the callee names a top-level binding.
		if b, ok := topLevelBinding(info, scope, callee); ok {
			if sigs := signaturesOf(info, b.DisplayType()); len(sigs) > 0 {
				return sigs
			}
		}
	case *ast.MemberExpr:
		// A method call likewise records only the arm it resolved to, so an overloaded
		// method reads its arms from the object's members.
		if obj, ok := info.MemberObject(info.ResolvedTypeOf(callee.Object)); ok {
			for _, elem := range obj.Elems {
				if m, ok := elem.(*soltype.MethodElem); ok && m.Name == callee.Prop.Name && len(m.Signatures) > 1 {
					return m.Signatures
				}
			}
		}
	}
	return signaturesOf(info, info.DisplayTypeOf(callee))
}

// topLevelBinding returns the binding ident reads when that binding is declared at the
// top level of a file or by a builtin. ok is false when ident reads a local, which scope
// cannot reach, or when scope is nil.
func topLevelBinding(info *solver.Info, scope *solver.Scope, ident *ast.IdentExpr) (solver.ValueBinding, bool) {
	if scope == nil {
		return solver.ValueBinding{}, false
	}
	b, ok := scope.GetValue(ident.Name)
	if !ok {
		return solver.ValueBinding{}, false
	}
	decl, declared := info.DeclOf(ident)
	if !declared {
		// Only a builtin has no declaration in the package.
		return b, len(b.Sources) == 0
	}
	for _, src := range b.Sources {
		if np, ok := src.(*ast.NodeProvenance); ok && np.Node == decl {
			return b, true
		}
	}
	return solver.ValueBinding{}, false
}

// signaturesOf returns the signatures a value of type t can be called with. A function
// has its own, an overload set rendered as an intersection has one per arm, and an object
// has its constructors, or its call signatures when it has no constructor.
func signaturesOf(info *solver.Info, t soltype.Type) []*soltype.FuncType {
	switch t := t.(type) {
	case *soltype.FuncType:
		return []*soltype.FuncType{t}
	case *soltype.IntersectionType:
		var sigs []*soltype.FuncType
		for _, part := range t.Types {
			sigs = append(sigs, signaturesOf(info, part)...)
		}
		return sigs
	case *soltype.ObjectType:
		var callable []*soltype.FuncType
		for _, elem := range t.Elems {
			switch elem := elem.(type) {
			case *soltype.ConstructorElem:
				return elem.Signatures
			case *soltype.CallableElem:
				callable = elem.Signatures
			}
		}
		return callable
	case nil:
		return nil
	}
	// An alias names the type it stands for, so it is expanded once.
	if expanded := info.Expand(t); expanded != t {
		return signaturesOf(info, expanded)
	}
	return nil
}

// calleeName returns the name a signature label starts with: the name callee reads, or
// "fn" when callee is not a name.
func calleeName(callee ast.Expr) string {
	switch callee := callee.(type) {
	case *ast.IdentExpr:
		return callee.Name
	case *ast.MemberExpr:
		return callee.Prop.Name
	}
	return "fn"
}

// signatureHelp builds the response for a call to name whose candidate signatures are
// sigs, with the cursor in argument active. The active signature is the first one with a
// parameter at that index.
func signatureHelp(name string, sigs []*soltype.FuncType, active int) *protocol.SignatureHelp {
	infos := make([]protocol.SignatureInformation, len(sigs))
	activeSig := -1
	for i, sig := range sigs {
		infos[i] = signatureInformation(name, sig)
		param, ok := activeParam(sig, active)
		infos[i].ActiveParameter = addr(protocol.UInteger(param))
		if ok && activeSig < 0 {
			activeSig = i
		}
	}
	if activeSig < 0 {
		activeSig = 0
	}
	return &protocol.SignatureHelp{
		Signatures:      infos,
		ActiveSignature: addr(protocol.UInteger(activeSig)),
		ActiveParameter: addr(protocol.UInteger(active)),
	}
}

// activeParam returns the index of the parameter of sig that argument arg binds to, and
// whether sig has one. A rest parameter binds every argument from its position on.
func activeParam(sig *soltype.FuncType, arg int) (int, bool) {
	n := len(sig.Params)
	if arg < n {
		return arg, true
	}
	if n > 0 && sig.Params[n-1].Rest {
		return n - 1, true
	}
	return arg, false
}

// signatureInformation renders sig as a call to name, such as
// `add(a: number, b: number) -> number`, with a label range per parameter.
func signatureInformation(name string, sig *soltype.FuncType) protocol.SignatureInformation {
	// A call passes no receiver, and the function's lifetime `'a fn` bounds where the
	// function may be kept rather than how it is called, so the label shows neither.
	shown := *sig
	shown.SelfParam = nil
	shown.Lt = nil
	rendered, ranges := soltype.PrintSignature(&shown)
	// The rendering starts with `fn `, which the callee's name replaces.
	label := name + strings.TrimPrefix(rendered, "fn ")
	shift := len(label) - len(rendered)

	params := make([]protocol.ParameterInformation, len(ranges))
	for i, r := range ranges {
		start, end := r[0]+shift, r[1]+shift
		params[i] = protocol.ParameterInformation{
			// LSP counts label offsets in UTF-16 code units.
			Label: []protocol.UInteger{
				protocol.UInteger(utf16Len(label[:start])),
				protocol.UInteger(utf16Len(label[:end])),
			},
		}
	}
	return protocol.SignatureInformation{Label: label, Parameters: params}
}

// utf16Len returns the number of UTF-16 code units s encodes to.
func utf16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}
