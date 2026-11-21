package main

import (
	"context"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/compiler"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/escalier-lang/escalier/internal/solver"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// spanToRange converts a span into the range an LSP client expects. A span
// holds byte offsets, so lineMap must be a map over the file the span indexes
// into. LSP numbers lines and characters from 0 and counts a character as a
// UTF-16 code unit, where Escalier numbers lines from 1.
func spanToRange(lineMap *ast.LineMap, span ast.Span) protocol.Range {
	startLine, startColumn := lineMap.Position(span.Start.Offset, ast.UTF16Columns)
	endLine, endColumn := lineMap.Position(span.End.Offset, ast.UTF16Columns)
	return protocol.Range{
		Start: protocol.Position{
			Line:      protocol.UInteger(startLine - 1),
			Character: protocol.UInteger(startColumn - 1),
		},
		End: protocol.Position{
			Line:      protocol.UInteger(endLine - 1),
			Character: protocol.UInteger(endColumn - 1),
		},
	}
}

// posToLoc converts a position an LSP client sent into a byte offset in the
// file it refers to, which is the file lineMap covers.
func posToLoc(lineMap *ast.LineMap, pos protocol.Position) ast.Location {
	// LSP numbers from 0 and Escalier from 1.
	line := int(pos.Line) + 1
	column := int(pos.Character) + 1
	return ast.Location{Offset: lineMap.Offset(line, column, ast.UTF16Columns)}
}

// lineMapForURI returns a map over the text of a document. An open document's
// text changes with every edit, so its map is built per request rather than
// kept. A request can also name a file the editor has not opened, and the text
// the last check parsed stands in for it.
func (s *Server) lineMapForURI(uri protocol.DocumentUri) *ast.LineMap {
	if doc, opened := s.openDocument(uri); opened {
		return ast.NewLineMap(doc.Text)
	}
	return s.checkedLineMap(s.sourceIDForURI(uri))
}

// checkedLineMap returns a map over the file a SourceID names in the last
// check's output. The lookup runs under the read lock, because validateBinScript
// replaces an entry in that map while requests are being served. Building the
// map happens after the lock is released, which is safe because Source.LineMap
// builds it once however many goroutines ask at the same time.
func (s *Server) checkedLineMap(sourceID int) *ast.LineMap {
	src := func() *ast.Source {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return checkedSource(s.checkOutput, sourceID)
	}()
	return lineMapFor(src)
}

// openDocument returns the document the editor has open at uri, and false when none is
// open there.
func (s *Server) openDocument(uri protocol.DocumentUri) (protocol.TextDocumentItem, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	doc, opened := s.documents[uri]
	return doc, opened
}

// checkedSource returns the file a SourceID names in a check output, or nil
// when the output holds none for that id.
func checkedSource(co *compiler.CheckOutput, sourceID int) *ast.Source {
	if co == nil {
		return nil
	}
	return co.SourceByID(sourceID)
}

// lineMapFor returns a map over a source, or over the empty string when the
// source is unknown.
func lineMapFor(src *ast.Source) *ast.LineMap {
	if src == nil {
		return ast.NewLineMap("")
	}
	return src.LineMap()
}

func (*Server) textDocumentDeclaration(context *glsp.Context, params *protocol.DeclarationParams) (any, error) {
	fmt.Fprintf(os.Stderr, "textDocumentDeclaration - uri = %s\n", params.TextDocument.URI)
	err := fmt.Errorf("textDocument/declaration not implemented yet")
	return nil, err
}

func (s *Server) textDocumentDefinition(context *glsp.Context, params *protocol.DefinitionParams) (any, error) {
	loc := posToLoc(s.lineMapForURI(params.TextDocument.URI), params.Position)
	sourceID := s.sourceIDForURI(params.TextDocument.URI)
	var node ast.Node
	var info *solver.Info
	var declSrc *ast.Source
	var ident *ast.IdentExpr
	var isIdent bool
	var span ast.Span
	func() {
		s.mu.RLock()
		defer s.mu.RUnlock()
		co := s.checkOutput
		if co != nil {
			node = findNodeAtLocation(co, sourceID, loc)
			info = co.InfoFor(sourceID)
		}
		ident, isIdent = node.(*ast.IdentExpr)
		if isIdent && info != nil {
			if decl, ok := info.DeclOf(ident); ok {
				declSrc = co.SourceByID(decl.Span().SourceID)
				span = definitionSpan(decl, declSrc)
			}
		}
	}()
	if node == nil {
		return nil, fmt.Errorf("textDocument/definition: node not found")
	}
	if !isIdent {
		return nil, fmt.Errorf("textDocument/definition: node is not an IdentExpr")
	}
	// A name bound by the standard library or a dependency is declared in a file
	// outside the package, which the check output holds no text for.
	if declSrc == nil {
		return nil, fmt.Errorf("textDocument/definition: no declaration in the package for %s", ident.Name)
	}

	rootPath := uriToPath(s.rootURI)
	return protocol.Location{
		URI:   protocol.DocumentUri(pathToURI(filepath.Join(rootPath, declSrc.Path))),
		Range: spanToRange(lineMapFor(declSrc), span),
	}, nil
}

// definitionSpan returns the span of the name decl binds, where decl is the node a
// binding records as its source and src is the file decl was parsed from. A `val` or
// `var` with a single name, a function, an identifier pattern, and a shorthand
// property pattern each yield their name. Any other node yields its own span.
func definitionSpan(decl ast.Node, src *ast.Source) ast.Span {
	switch decl := decl.(type) {
	case *ast.VarDecl:
		if ip, ok := decl.Pattern.(*ast.IdentPat); ok {
			return identPatNameSpan(ip, src)
		}
	case *ast.FuncDecl:
		if decl.Name != nil {
			return decl.Name.Span()
		}
	case *ast.IdentPat:
		return identPatNameSpan(decl, src)
	case *ast.ObjShorthandPat:
		return decl.Key.Span()
	}
	return decl.Span()
}

// identPatNameSpan returns the span of the name ip binds, found in src's text. The
// pattern keeps no span of the name alone. Its own span can start before the name, at a
// `mut` or at the `&mut` of a constructor's `&mut self`, and can run past it over a type
// annotation and a default. The name is the last occurrence of ip.Name before the
// annotation or the default, since nothing but punctuation and whitespace sits between
// them.
func identPatNameSpan(ip *ast.IdentPat, src *ast.Source) ast.Span {
	span := ip.Span()
	start := span.Start.Offset
	limit := span.End.Offset
	if ip.TypeAnn != nil {
		limit = ip.TypeAnn.Span().Start.Offset
	} else if ip.Default != nil {
		limit = ip.Default.Span().Start.Offset
	}
	if src != nil && start <= limit && limit <= len(src.Contents) {
		if i := strings.LastIndex(src.Contents[start:limit], ip.Name); i >= 0 {
			start += i
		}
	}
	return ast.Span{
		Start:    ast.Location{Offset: start},
		End:      ast.Location{Offset: start + len(ip.Name)},
		SourceID: span.SourceID,
	}
}

func (s *Server) textDocumentTypeDefinition(context *glsp.Context, params *protocol.TypeDefinitionParams) (any, error) {
	fmt.Fprintf(os.Stderr, "textDocumentTypeDefinition - uri = %s\n", params.TextDocument.URI)
	err := fmt.Errorf("textDocument/typeDefinition not implemented yet")
	return nil, err
}

func (s *Server) textDocumentDidOpen(context *glsp.Context, params *protocol.DidOpenTextDocumentParams) error {
	func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.documents[params.TextDocument.URI] = params.TextDocument
		if params.TextDocument.LanguageID == "escalier" {
			s.packageGen++
			if s.isModuleFile(params.TextDocument.URI) {
				s.libGen++
			}
		}
	}()
	if params.TextDocument.LanguageID == "escalier" {
		s.validate(context, params.TextDocument.URI, params.TextDocument.Text, params.TextDocument.Version)
	}
	return nil
}

func (s *Server) textDocumentDidChange(context *glsp.Context, params *protocol.DidChangeTextDocumentParams) error {
	doc, err := func() (protocol.TextDocumentItem, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		doc := s.documents[params.TextDocument.URI]

		for _, change := range params.ContentChanges {
			switch change := change.(type) {
			case protocol.TextDocumentContentChangeEvent:
				return doc, fmt.Errorf("incremental changes not supported")
			case protocol.TextDocumentContentChangeEventWhole:
				s.documents[params.TextDocument.URI] = protocol.TextDocumentItem{
					URI:        params.TextDocument.URI,
					LanguageID: doc.LanguageID,
					Version:    params.TextDocument.Version,
					Text:       change.Text,
				}
			}
		}
		if doc.LanguageID == "escalier" {
			s.packageGen++
			if s.isModuleFile(params.TextDocument.URI) {
				s.libGen++
			}
		}
		return doc, nil
	}()
	if err != nil {
		return err
	}

	if doc.LanguageID == "escalier" {
		// Use only the last content change since we're in full-sync mode
		// and only the final state matters.
		lastChange := params.ContentChanges[len(params.ContentChanges)-1].(protocol.TextDocumentContentChangeEventWhole)
		go s.validate(context, params.TextDocument.URI, lastChange.Text, params.TextDocument.Version)
	}
	return nil
}

// textDocumentHover returns the hover at verbosity level 0 for the node at the cursor, or
// nil when the cursor is on nothing with a hover to show.
func (server *Server) textDocumentHover(context *glsp.Context, params *protocol.HoverParams) (*protocol.Hover, error) {
	h, err := server.textDocumentVerboseHover(context, &hoverParams{HoverParams: *params})
	if h == nil || err != nil {
		return nil, err
	}
	return &protocol.Hover{Contents: h.Contents}, nil
}

// textDocumentVerboseHover returns the hover at the level params asks for, for the node
// at the cursor, or nil when the cursor is on nothing with a hover to show. Only a hover
// on a type alias's name or on a reference to a type alias has levels above 0.
func (server *Server) textDocumentVerboseHover(context *glsp.Context, params *hoverParams) (*verboseHover, error) {
	loc := posToLoc(server.lineMapForURI(params.TextDocument.URI), params.Position)
	sourceID := server.sourceIDForURI(params.TextDocument.URI)
	q := hoverQuery{loc: loc}
	func() {
		server.mu.RLock()
		defer server.mu.RUnlock()
		if co := server.checkOutput; co != nil {
			q.node, q.ancestors = findNodeWithAncestorsAt(co, sourceID, loc)
			q.info = co.InfoFor(sourceID)
			q.infoFor = co.InfoFor
			q.scope = scopeOfFile(co, sourceID)
		}
	}()
	if q.info == nil {
		return nil, nil
	}
	// A hover on a type alias's name or on a type reference evaluates the alias's type,
	// and deciding a conditional in it writes the run's Context while it runs. Only those
	// hovers hold solverMu for writing.
	if evaluatesAlias(q.node) {
		server.solverMu.Lock()
		defer server.solverMu.Unlock()
	} else {
		server.solverMu.RLock()
		defer server.solverMu.RUnlock()
	}
	if h, ok := aliasHover(q, params.VerbosityLevel); ok {
		return h, nil
	}
	if text, ok := typeHover(q); ok {
		return newVerboseHover(text), nil
	}
	if text, ok := valueHover(q); ok {
		return newVerboseHover(text), nil
	}
	if text, ok := selfHover(q); ok {
		return newVerboseHover(text), nil
	}
	if text, ok := memberHover(q); ok {
		return newVerboseHover(text), nil
	}
	if text, ok := namespaceHover(q); ok {
		return newVerboseHover(text), nil
	}
	if text, ok := declMemberHover(q); ok {
		return newVerboseHover(text), nil
	}
	if text, ok := importHover(q); ok {
		return newVerboseHover(text), nil
	}
	if text, ok := patternHover(q); ok {
		return newVerboseHover(text), nil
	}
	if text, ok := typeOfHover(q); ok {
		return newVerboseHover(text), nil
	}
	switch q.node.(type) {
	// A literal or template string already shows its value in the source, so a hover
	// on it adds nothing.
	case *ast.LiteralExpr, *ast.TemplateLitExpr, *ast.LitPat:
		return nil, nil
	case ast.Expr, ast.Pat:
		if t := q.info.DisplayTypeOf(q.node); t != nil {
			return newVerboseHover(hoverText(q.info, q.node, t)), nil
		}
	}
	return nil, nil
}

// evaluatesAlias reports whether a hover on node can evaluate a type alias's type, which
// it does on a type declaration and on a type reference.
func evaluatesAlias(node ast.Node) bool {
	switch node.(type) {
	case *ast.TypeDecl, *ast.TypeRefTypeAnn:
		return true
	}
	return false
}

// codeHover returns a hover that shows code as an Escalier code block.
func codeHover(code string) *protocol.Hover {
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.MarkupKindMarkdown,
			Value: "```escalier\n" + code + "\n```",
		},
	}
}

// hoverText renders the hover for node, whose display type is t. A binding, or a read
// of one, renders as `name: T`. A mutable binding shows that on its name as
// `mut p: {x: number}`, since a value's mutability belongs to its binding rather than
// its type. Any other node renders as its type alone.
func hoverText(info *solver.Info, node ast.Node, t soltype.Type) string {
	name, mut, ok := bindingOf(info, node)
	if !ok {
		return soltype.PrintAsScheme(t)
	}
	return bindingText(name, mut, t)
}

// bindingText renders a binding of name to a value of type t, as `p: {x: number}`, or as
// `mut p: {x: number}` when mut is true.
func bindingText(name string, mut bool, t soltype.Type) string {
	text := name + ": " + soltype.PrintAsScheme(t)
	if mut {
		return "mut " + text
	}
	return text
}

// bindingOf returns the name node binds or reads, and whether that binding is mutable.
// ok is false when node neither binds a name nor reads one a `val`, `var`, or parameter
// binding declared.
func bindingOf(info *solver.Info, node ast.Node) (name string, mut bool, ok bool) {
	switch n := node.(type) {
	case *ast.IdentPat:
		return n.Name, info.BindsMut(n), true
	case *ast.IdentExpr:
		decl, found := info.DeclOf(n)
		if !found {
			return "", false, false
		}
		switch decl := decl.(type) {
		case *ast.VarDecl:
			// A module-level read resolves to the whole declaration, so find the leaf of its
			// pattern that binds the name.
			mut := false
			ast.ForEachLeafBinding(decl.Pattern, func(name string, leaf ast.Node) {
				if name != n.Name {
					return
				}
				switch leaf := leaf.(type) {
				case *ast.IdentPat:
					mut = info.BindsMut(leaf)
				case *ast.ObjShorthandPat:
					mut = leaf.Mutable
				}
			})
			return n.Name, mut, true
		case *ast.IdentPat:
			return n.Name, info.BindsMut(decl), true
		case *ast.ObjShorthandPat:
			return n.Name, decl.Mutable, true
		}
	}
	return "", false, false
}

func addr[T any](x T) *T {
	return &x
}

func (s *Server) textDocumentCodeAction(context *glsp.Context, params *protocol.CodeActionParams) (any, error) {
	compileAction := protocol.CodeAction{
		Title:       "Compile",
		Kind:        addr("compile"),
		Diagnostics: []protocol.Diagnostic{},
		IsPreferred: nil,
		Disabled:    nil,
		Edit:        nil, // Require the client to make a workspace/executeCommand request
		Command: &protocol.Command{
			Title:     "Compile",
			Command:   "compile",
			Arguments: []any{},
		},
		Data: nil,
	}

	codeActions := []protocol.CodeAction{}
	if wantsCodeActionKind(params.Context.Only, "compile") {
		codeActions = append(codeActions, compileAction)
	}
	if wantsCodeActionKind(params.Context.Only, protocol.CodeActionKindQuickFix) {
		for _, diag := range params.Context.Diagnostics {
			if action, ok := borrowFixAction(params.TextDocument.URI, diag); ok {
				codeActions = append(codeActions, action)
			}
		}
	}
	codeActions = append(codeActions, s.fillArmsActions(
		params.TextDocument.URI, params.Range, params.Context.Diagnostics, params.Context.Only)...)
	if wantsCodeActionKind(params.Context.Only, signatureActionKind) {
		if action, ok := s.signatureActionAt(params.TextDocument.URI, params.Range.Start); ok {
			codeActions = append(codeActions, action)
		}
	}

	return codeActions, nil
}

// wantsCodeActionKind reports whether a code action request asks for actions of kind. only is
// the `only` list of the request's context, and an empty list asks for every kind. A listed
// kind also covers the kinds nested under it, so `quickfix` covers `quickfix.borrow`.
func wantsCodeActionKind(only []protocol.CodeActionKind, kind protocol.CodeActionKind) bool {
	if len(only) == 0 {
		return true
	}
	for _, k := range only {
		if kind == k || strings.HasPrefix(string(kind), string(k)+".") {
			return true
		}
	}
	return false
}

// isCacheStale returns true if the document has been updated since the last
// successful validation, or if any sibling file has changed since the last
// package-level validation.
// Must be called while holding mu.RLock().
func (s *Server) isCacheStale(uri protocol.DocumentUri) bool {
	doc, ok := s.documents[uri]
	if !ok {
		return false
	}
	validated, ok := s.validatedVersion[uri]
	if !ok {
		return true
	}
	if doc.Version != validated {
		return true
	}
	if s.packageGen != s.packageValidatedGen {
		return true
	}
	return false
}

// waitForFreshCheck blocks until the cached check output reflects the latest text of
// uri, until the check of that text panics, or until 2s pass, whichever comes first. The
// caller holds s.mu's read lock, which the wait releases while it sleeps and holds again
// when it returns.
func (s *Server) waitForFreshCheck(uri protocol.DocumentUri) {
	deadline := time.Now().Add(2 * time.Second)
	for s.isCacheStale(uri) && !s.checkPanicked(uri) && time.Now().Before(deadline) {
		// Sleeps until validation calls `Broadcast()`.
		s.validated.Wait()
	}
}

// checkPanicked reports whether the check of uri's latest text panicked. The caller holds
// s.mu.
func (s *Server) checkPanicked(uri protocol.DocumentUri) bool {
	panicked, ok := s.panickedVersion[uri]
	return ok && panicked == s.documents[uri].Version
}

// isModuleFile checks if a URI corresponds to a file under the lib/ directory.
func (s *Server) isModuleFile(uri protocol.DocumentUri) bool {
	if s.rootURI == "" {
		return false
	}
	rootPath := uriToPath(s.rootURI)
	filePath := uriToPath(string(uri))
	rel, err := filepath.Rel(rootPath, filePath)
	if err != nil {
		return false
	}
	return strings.HasPrefix(rel, "lib/") || strings.HasPrefix(rel, "lib\\")
}

// findLibFiles discovers all .esc files in the lib/ directory under the workspace root.
func (s *Server) findLibFiles() ([]string, error) {
	rootPath := uriToPath(s.rootURI)
	libDir := filepath.Join(rootPath, "lib")

	var files []string
	err := filepath.WalkDir(libDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".esc") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	return files, nil
}

// refreshLibFilesCache scans lib/ and stores absolute .esc file paths in memory.
func (s *Server) refreshLibFilesCache() error {
	files, err := s.findLibFiles()
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.libFilesCache = set.FromSlice(files)

	return nil
}

// cachedLibFilesSnapshot returns a stable snapshot of cached lib file paths.
func (s *Server) cachedLibFilesSnapshot() []string {
	files := func() []string {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.libFilesCache.ToSlice()
	}()

	sort.Strings(files)
	return files
}

// stableSourceID returns a deterministic integer ID for a relative file path.
// This ensures IDs remain stable across re-parses regardless of file discovery
// order or files being added/removed.
func stableSourceID(relPath string) int {
	h := fnv.New32a()
	h.Write([]byte(relPath))
	return int(h.Sum32())
}

// sourceIDForURI computes the stable source ID for a document URI.
func (s *Server) sourceIDForURI(uri protocol.DocumentUri) int {
	rootPath := uriToPath(s.rootURI)
	filePath := uriToPath(string(uri))
	rel, err := filepath.Rel(rootPath, filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sourceIDForURI: filepath.Rel(%s, %s): %s\n", rootPath, filePath, err)
		return stableSourceID(filePath)
	}
	return stableSourceID(filepath.ToSlash(rel))
}

func (server *Server) validate(lspContext *glsp.Context, uri protocol.DocumentUri, contents string, version protocol.Integer) {
	// textDocumentDidChange runs validate on a goroutine of its own, where a panic would
	// end the process, so the check recovers here rather than in Handle.
	defer func() {
		if value := recover(); value != nil {
			server.handleCheckPanic(lspContext, uri, version, panicReport{value: value, stack: debug.Stack()})
		}
	}()

	// Check staleness before doing expensive work. handleCheckPanic takes mu for
	// writing, so the read lock is released by a deferred unlock.
	var currentDoc protocol.TextDocumentItem
	var canIncrCheck bool
	var cachedLib compiler.LibScope
	var snapshotPackageGen, snapshotLibGen int64
	func() {
		server.mu.RLock()
		defer server.mu.RUnlock()
		currentDoc = server.documents[uri]
		canIncrCheck = server.canCheckScriptAlone(uri)
		if canIncrCheck {
			cachedLib = server.checkOutput.LibScope
		}
		snapshotPackageGen = server.packageGen
		snapshotLibGen = server.libGen
	}()
	if currentDoc.Version != version {
		server.validated.Broadcast()
		return
	}

	rootPath := uriToPath(server.rootURI)

	if canIncrCheck {
		// Fast path: only a bin/ file changed and lib/ is unchanged.
		// Re-check just this one script against the cached lib surface.
		server.validateBinScript(lspContext, uri, contents, version, rootPath, cachedLib, snapshotPackageGen, snapshotLibGen)
		return
	}

	// Slow path: full package check.
	server.validateFull(lspContext, uri, contents, version, rootPath, snapshotPackageGen, snapshotLibGen)
}

// canCheckScriptAlone reports whether a change to uri can be checked by re-checking that
// one file against the cached library run, rather than by checking the whole package.
// The caller holds mu.
func (server *Server) canCheckScriptAlone(uri protocol.DocumentUri) bool {
	isBinFile := !server.isModuleFile(uri)
	// We can incrementally re-check just this one bin/ script (instead of
	// doing a full package check) when all four conditions hold:
	//   1. The file is a bin/ script (lib/ changes always need a full check).
	//   2. A prior full check has run (checkOutput != nil), so we have a
	//      cached lib surface to type-check the script against.
	//   3. No lib/ files have changed since that full check
	//      (libGen == libValidatedGen), so the cached surface is still valid.
	//   4. No check has panicked since that full check (see needsFullCheck).
	return isBinFile && server.checkOutput != nil && server.libGen == server.libValidatedGen &&
		!server.needsFullCheck
}

// validateBinScript re-checks a single bin/ script against the cached library
// surface, avoiding re-parsing and re-checking all lib/ files.
func (server *Server) validateBinScript(
	lspContext *glsp.Context,
	uri protocol.DocumentUri,
	contents string,
	version protocol.Integer,
	rootPath string,
	lib compiler.LibScope,
	snapshotPackageGen int64,
	snapshotLibGen int64,
) {
	triggerSourceID := server.sourceIDForURI(uri)
	rel, err := filepath.Rel(rootPath, uriToPath(string(uri)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "validateBinScript: filepath.Rel: %s\n", err)
		server.validated.Broadcast()
		return
	}

	src := &ast.Source{
		ID:       triggerSourceID,
		Path:     filepath.ToSlash(rel),
		Contents: contents,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := func() compiler.BinScriptOutput {
		// The unlock is deferred so that a panic in the checker still releases the lock.
		server.solverMu.Lock()
		defer server.solverMu.Unlock()
		server.runTestHooks(contents)
		return compiler.CheckBinScript(ctx, lib, src, packageConfig(rootPath))
	}()

	// Verify staleness and update caches. handleCheckPanic takes mu for writing, so the
	// unlock is deferred to release mu before a panic here reaches it.
	stale := func() bool {
		server.mu.Lock()
		defer server.mu.Unlock()
		currentDoc := server.documents[uri]
		if currentDoc.Version != version || server.packageGen != snapshotPackageGen || server.libGen != snapshotLibGen {
			return true
		}
		server.updateScriptCheck(uri, version, triggerSourceID, src, result)
		return false
	}()
	server.validated.Broadcast()
	if stale {
		return
	}

	// Publish diagnostics for just this file.
	server.publishDiagnosticsForScript(lspContext, uri, version, triggerSourceID, src, result.ParseErrors, result.TypeErrors)
}

// updateScriptCheck replaces the entries for the bin/ script sourceID in the cached check
// output with result, the re-check of src, and records uri as checked at version. The
// caller holds mu for writing.
func (server *Server) updateScriptCheck(
	uri protocol.DocumentUri,
	version protocol.Integer,
	triggerSourceID int,
	src *ast.Source,
	result compiler.BinScriptOutput,
) {
	co := server.checkOutput
	// Every value is computed before any is assigned, so a panic partway leaves
	// checkOutput as it was rather than holding this script's AST beside another
	// revision's errors.
	parseErrors := append(filterOutSourceID(co.ParseErrors, triggerSourceID), result.ParseErrors...)
	typeErrors := append(filterOutTypeErrors(co.TypeErrors, triggerSourceID), result.TypeErrors...)
	// Update only this script's entries in the existing checkOutput.
	co.Scripts[triggerSourceID] = result.Script
	co.ScriptInfos[triggerSourceID] = result.Info
	co.ScriptScopes[triggerSourceID] = result.Scope
	// The new AST's spans index the text just parsed, so the source has to move
	// with them. Keeping the one the last full check stored would convert the
	// new offsets against stale text.
	if co.Sources == nil {
		co.Sources = map[int]*ast.Source{}
	}
	co.Sources[triggerSourceID] = src
	co.ParseErrors = parseErrors
	co.TypeErrors = typeErrors
	server.packageValidatedGen = server.packageGen
	server.validatedVersion[uri] = version
}

// packageConfig reads the PackageConfig from the package.json in rootPath. A file that
// cannot be read or parsed is logged and the default config used, so the editor keeps
// checking while the user fixes it.
func packageConfig(rootPath string) compiler.PackageConfig {
	config, err := compiler.ReadPackageConfig(rootPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "validate: %v\n", err)
	}
	return config
}

// validateFull performs a full package check (lib/ + bin/).
func (server *Server) validateFull(
	lspContext *glsp.Context,
	uri protocol.DocumentUri,
	contents string,
	version protocol.Integer,
	rootPath string,
	snapshotPackageGen int64,
	snapshotLibGen int64,
) {
	// Collect all source files (lib/ + bin/) with in-memory content for open docs.
	sources, err := server.collectSources(rootPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "validate: failed to collect sources: %v\n", err)
		server.validated.Broadcast()
		return
	}

	// Override the current file's content with the latest from the editor,
	// since collectSources reads from s.documents which may have been
	// updated to a newer version than the one we're validating.
	triggerSourceID := server.sourceIDForURI(uri)
	for _, src := range sources {
		if src.ID == triggerSourceID {
			src.Contents = contents
			break
		}
	}

	// Run package-level type checking (no codegen).
	server.runTestHooks(contents)
	output := compiler.CheckPackage(sources, packageConfig(rootPath))

	// Verify that no document versions changed during validation. handleCheckPanic takes
	// mu for writing, so the unlock is deferred to release mu before a panic here reaches
	// it.
	stale := func() bool {
		server.mu.Lock()
		defer server.mu.Unlock()
		currentDoc := server.documents[uri]
		if currentDoc.Version != version || server.packageGen != snapshotPackageGen || server.libGen != snapshotLibGen {
			return true
		}
		server.recordFullCheck(uri, version, &output)
		return false
	}()
	server.validated.Broadcast()
	if stale {
		return
	}

	// Publish diagnostics for all files.
	severity := protocol.DiagnosticSeverityError
	source := "escalier"
	diagsBySourceID := make(map[int][]protocol.Diagnostic)
	for _, err := range output.ParseErrors {
		diagsBySourceID[err.Span.SourceID] = append(diagsBySourceID[err.Span.SourceID], protocol.Diagnostic{
			Range:    spanToRange(lineMapFor(checkedSource(&output, err.Span.SourceID)), err.Span),
			Severity: &severity,
			Source:   &source,
			Message:  err.Message,
		})
	}
	for _, err := range output.TypeErrors {
		sourceID := err.Span().SourceID
		diagsBySourceID[sourceID] = append(diagsBySourceID[sourceID],
			toDiagnostic(err, checkedSource(&output, sourceID)))
	}

	// Publish diagnostics for all files. Guard against nil context
	// (e.g., in tests without an LSP connection).
	if lspContext.Notify == nil {
		return
	}
	server.mu.RLock()
	defer server.mu.RUnlock()
	if output.Module != nil {
		for _, file := range output.Module.Files {
			fileURI := protocol.DocumentUri(pathToURI(filepath.Join(rootPath, file.Path)))
			fileDiags := emptyIfNil(diagsBySourceID[file.SourceID])
			params := &protocol.PublishDiagnosticsParams{
				URI:         fileURI,
				Diagnostics: fileDiags,
			}
			if ver, ok := server.validatedVersion[fileURI]; ok {
				v := protocol.UInteger(ver)
				params.Version = &v
			}
			go lspContext.Notify(protocol.ServerTextDocumentPublishDiagnostics, params)
		}
	}
	// Publish diagnostics for bin/ files.
	sourcePathByID := make(map[int]string, len(sources))
	for _, src := range sources {
		sourcePathByID[src.ID] = src.Path
	}
	for srcID := range output.Scripts {
		path, ok := sourcePathByID[srcID]
		if !ok {
			continue
		}
		fileURI := protocol.DocumentUri(pathToURI(filepath.Join(rootPath, path)))
		fileDiags := emptyIfNil(diagsBySourceID[srcID])
		params := &protocol.PublishDiagnosticsParams{
			URI:         fileURI,
			Diagnostics: fileDiags,
		}
		if ver, ok := server.validatedVersion[fileURI]; ok {
			v := protocol.UInteger(ver)
			params.Version = &v
		}
		go lspContext.Notify(protocol.ServerTextDocumentPublishDiagnostics, params)
	}
}

// recordFullCheck stores output, the full check of the package that uri at version
// started, as the cached check output, and records every open document as checked. The
// caller holds mu for writing.
func (server *Server) recordFullCheck(uri protocol.DocumentUri, version protocol.Integer, output *compiler.CheckOutput) {
	server.checkOutput = output
	server.needsFullCheck = false
	server.packageValidatedGen = server.packageGen
	server.libValidatedGen = server.libGen
	server.validatedVersion[uri] = version
	// Also update validatedVersion for all other open Escalier documents.
	// This iterates server.documents rather than sources because all open
	// lib/ and bin/ .esc files are included in sources (collectSources uses
	// the editor buffer for open files). If a non-package file somehow gets
	// stamped, the only effect is that isCacheStale returns false for it,
	// which is harmless — there is nothing to wait for since it won't be
	// re-validated through the full-package path.
	for docURI, doc := range server.documents {
		if docURI != uri && doc.LanguageID == "escalier" {
			server.validatedVersion[docURI] = doc.Version
		}
	}
}

// publishDiagnosticsForScript publishes diagnostics for a single bin/ script.
func (server *Server) publishDiagnosticsForScript(
	lspContext *glsp.Context,
	uri protocol.DocumentUri,
	version protocol.Integer,
	sourceID int,
	src *ast.Source,
	parseErrors []*parser.Error,
	typeErrors []compiler.Diagnostic,
) {
	if lspContext.Notify == nil {
		return
	}
	diags := fileDiagnostics(sourceID, src, parseErrors, typeErrors)

	diagVersion := protocol.UInteger(version)

	go lspContext.Notify(protocol.ServerTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: emptyIfNil(diags),
		Version:     &diagVersion,
	})
}

// fileDiagnostics returns the diagnostics for the errors among parseErrors and typeErrors
// that belong to the file sourceID. src is that file, or nil when the check output holds
// none for it.
func fileDiagnostics(sourceID int, src *ast.Source, parseErrors []*parser.Error, typeErrors []compiler.Diagnostic) []protocol.Diagnostic {
	severity := protocol.DiagnosticSeverityError
	source := "escalier"
	var diags []protocol.Diagnostic
	for _, err := range parseErrors {
		if err.Span.SourceID == sourceID {
			diags = append(diags, protocol.Diagnostic{
				Range:    spanToRange(lineMapFor(src), err.Span),
				Severity: &severity,
				Source:   &source,
				Message:  err.Message,
			})
		}
	}
	for _, err := range typeErrors {
		if err.Span().SourceID == sourceID {
			diags = append(diags, toDiagnostic(err, src))
		}
	}
	return diags
}

// toDiagnostic converts a type error into the diagnostic published for it. src is the file
// the error's span indexes into, or nil when the check output holds none.
func toDiagnostic(err compiler.Diagnostic, src *ast.Source) protocol.Diagnostic {
	severity := protocol.DiagnosticSeverityError
	if compiler.IsWarning(err) {
		severity = protocol.DiagnosticSeverityWarning
	}
	source := "escalier"
	diag := protocol.Diagnostic{
		Range:    spanToRange(lineMapFor(src), err.Span()),
		Severity: &severity,
		Source:   &source,
		Message:  err.Message(),
	}
	if data, ok := borrowFixDataFor(err, src); ok {
		diag.Data = data
	}
	return diag
}

// filterOutSourceID removes parse errors belonging to the given sourceID.
func filterOutSourceID(errs []*parser.Error, sourceID int) []*parser.Error {
	result := make([]*parser.Error, 0, len(errs))
	for _, e := range errs {
		if e.Span.SourceID != sourceID {
			result = append(result, e)
		}
	}
	return result
}

// filterOutTypeErrors removes type errors belonging to the given sourceID.
func filterOutTypeErrors(errs []compiler.Diagnostic, sourceID int) []compiler.Diagnostic {
	result := make([]compiler.Diagnostic, 0, len(errs))
	for _, e := range errs {
		if e.Span().SourceID != sourceID {
			result = append(result, e)
		}
	}
	return result
}

// emptyIfNil returns an empty slice if the input is nil, ensuring JSON
// serialization produces [] instead of null.
func emptyIfNil(diags []protocol.Diagnostic) []protocol.Diagnostic {
	if diags == nil {
		return []protocol.Diagnostic{}
	}
	return diags
}
