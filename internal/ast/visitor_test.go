package ast

import (
	"testing"
)

// Mock visitor that tracks method calls
type mockVisitor struct {
	enterCalls []string
	exitCalls  []string
	skipNodes  map[string]bool // nodes to skip (return false from Enter)
}

func newMockVisitor() *mockVisitor {
	return &mockVisitor{
		enterCalls: make([]string, 0),
		exitCalls:  make([]string, 0),
		skipNodes:  make(map[string]bool),
	}
}

func (v *mockVisitor) skipNode(nodeType string) {
	v.skipNodes[nodeType] = true
}

func (v *mockVisitor) EnterLit(l Lit) bool {
	v.enterCalls = append(v.enterCalls, "EnterLit")
	return !v.skipNodes["Lit"]
}

func (v *mockVisitor) EnterPat(p Pat) bool {
	v.enterCalls = append(v.enterCalls, "EnterPat")
	return !v.skipNodes["Pat"]
}

func (v *mockVisitor) EnterExpr(e Expr) bool {
	v.enterCalls = append(v.enterCalls, "EnterExpr")
	return !v.skipNodes["Expr"]
}

func (v *mockVisitor) EnterObjExprElem(e ObjExprElem) bool {
	v.enterCalls = append(v.enterCalls, "EnterObjExprElem")
	return !v.skipNodes["ObjExprElem"]
}

func (v *mockVisitor) EnterStmt(s Stmt) bool {
	v.enterCalls = append(v.enterCalls, "EnterStmt")
	return !v.skipNodes["Stmt"]
}

func (v *mockVisitor) EnterDecl(d Decl) bool {
	v.enterCalls = append(v.enterCalls, "EnterDecl")
	return !v.skipNodes["Decl"]
}

func (v *mockVisitor) EnterTypeAnn(t TypeAnn) bool {
	v.enterCalls = append(v.enterCalls, "EnterTypeAnn")
	return !v.skipNodes["TypeAnn"]
}

func (v *mockVisitor) EnterBlock(b Block) bool {
	v.enterCalls = append(v.enterCalls, "EnterBlock")
	return !v.skipNodes["Block"]
}

func (v *mockVisitor) ExitLit(l Lit) {
	v.exitCalls = append(v.exitCalls, "ExitLit")
}

func (v *mockVisitor) ExitPat(p Pat) {
	v.exitCalls = append(v.exitCalls, "ExitPat")
}

func (v *mockVisitor) ExitExpr(e Expr) {
	v.exitCalls = append(v.exitCalls, "ExitExpr")
}

func (v *mockVisitor) ExitObjExprElem(e ObjExprElem) {
	v.exitCalls = append(v.exitCalls, "ExitObjExprElem")
}

func (v *mockVisitor) ExitStmt(s Stmt) {
	v.exitCalls = append(v.exitCalls, "ExitStmt")
}

func (v *mockVisitor) ExitDecl(d Decl) {
	v.exitCalls = append(v.exitCalls, "ExitDecl")
}

func (v *mockVisitor) ExitTypeAnn(t TypeAnn) {
	v.exitCalls = append(v.exitCalls, "ExitTypeAnn")
}

func (v *mockVisitor) ExitBlock(b Block) {
	v.exitCalls = append(v.exitCalls, "ExitBlock")
}

func TestDefaultVisitor_AllEnterMethodsReturnTrue(t *testing.T) {
	visitor := &DefaultVisitor{}

	// Test all Enter methods return true
	if !visitor.EnterLit(nil) {
		t.Error("EnterLit should return true")
	}
	if !visitor.EnterPat(nil) {
		t.Error("EnterPat should return true")
	}
	if !visitor.EnterExpr(nil) {
		t.Error("EnterExpr should return true")
	}
	if !visitor.EnterObjExprElem(nil) {
		t.Error("EnterObjExprElem should return true")
	}
	if !visitor.EnterStmt(nil) {
		t.Error("EnterStmt should return true")
	}
	if !visitor.EnterDecl(nil) {
		t.Error("EnterDecl should return true")
	}
	if !visitor.EnterTypeAnn(nil) {
		t.Error("EnterTypeAnn should return true")
	}
	if !visitor.EnterBlock(Block{Stmts: nil, Span: Span{Start: Location{Line: 0, Column: 0}, End: Location{Line: 0, Column: 0}, SourceID: 0}}) {
		t.Error("EnterBlock should return true")
	}
}

func TestDefaultVisitor_ExitMethodsDoNotPanic(t *testing.T) {
	visitor := &DefaultVisitor{}

	// Test all Exit methods can be called without panicking
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Exit methods should not panic: %v", r)
		}
	}()

	visitor.ExitLit(nil)
	visitor.ExitPat(nil)
	visitor.ExitExpr(nil)
	visitor.ExitObjExprElem(nil)
	visitor.ExitStmt(nil)
	visitor.ExitDecl(nil)
	visitor.ExitTypeAnn(nil)
	visitor.ExitBlock(Block{Stmts: nil, Span: Span{Start: Location{Line: 0, Column: 0}, End: Location{Line: 0, Column: 0}, SourceID: 0}})
}

func TestEmptyExpr_Accept(t *testing.T) {
	visitor := newMockVisitor()
	span := Span{Start: Location{Line: 1, Column: 0}, End: Location{Line: 1, Column: 0}, SourceID: 0}
	expr := NewEmpty(span)

	expr.Accept(visitor)

	expectedEnter := []string{"EnterExpr"}
	expectedExit := []string{"ExitExpr"}

	if len(visitor.enterCalls) != len(expectedEnter) {
		t.Errorf("Expected %d enter calls, got %d", len(expectedEnter), len(visitor.enterCalls))
	}
	if len(visitor.exitCalls) != len(expectedExit) {
		t.Errorf("Expected %d exit calls, got %d", len(expectedExit), len(visitor.exitCalls))
	}

	for i, expected := range expectedEnter {
		if i >= len(visitor.enterCalls) || visitor.enterCalls[i] != expected {
			t.Errorf("Expected enter call %d to be %s, got %s", i, expected, visitor.enterCalls[i])
		}
	}

	for i, expected := range expectedExit {
		if i >= len(visitor.exitCalls) || visitor.exitCalls[i] != expected {
			t.Errorf("Expected exit call %d to be %s, got %s", i, expected, visitor.exitCalls[i])
		}
	}
}

func TestBinaryExpr_Accept_TraversesChildren(t *testing.T) {
	visitor := newMockVisitor()
	span := Span{Start: Location{Line: 1, Column: 0}, End: Location{Line: 1, Column: 10}, SourceID: 0}

	// Create a binary expression: left + right
	left := NewEmpty(Span{Start: Location{Line: 1, Column: 0}, End: Location{Line: 1, Column: 4}, SourceID: 0})
	right := NewEmpty(Span{Start: Location{Line: 1, Column: 6}, End: Location{Line: 1, Column: 10}, SourceID: 0})
	binary := NewBinary(left, right, Plus, span)

	binary.Accept(visitor)

	// Should have 3 EnterExpr calls: binary, left, right
	// And 3 ExitExpr calls in reverse order: left, right, binary
	expectedEnterCount := 3
	expectedExitCount := 3

	if len(visitor.enterCalls) != expectedEnterCount {
		t.Errorf("Expected %d enter calls, got %d", expectedEnterCount, len(visitor.enterCalls))
	}
	if len(visitor.exitCalls) != expectedExitCount {
		t.Errorf("Expected %d exit calls, got %d", expectedExitCount, len(visitor.exitCalls))
	}

	// Check that all enter calls are EnterExpr
	for i, call := range visitor.enterCalls {
		if call != "EnterExpr" {
			t.Errorf("Expected enter call %d to be EnterExpr, got %s", i, call)
		}
	}

	// Check that all exit calls are ExitExpr
	for i, call := range visitor.exitCalls {
		if call != "ExitExpr" {
			t.Errorf("Expected exit call %d to be ExitExpr, got %s", i, call)
		}
	}
}

func TestBinaryExpr_Accept_SkipsChildrenWhenEnterReturnsFalse(t *testing.T) {
	visitor := newMockVisitor()
	visitor.skipNode("Expr") // Skip all expressions

	span := Span{Start: Location{Line: 1, Column: 0}, End: Location{Line: 1, Column: 10}, SourceID: 0}
	left := NewEmpty(Span{Start: Location{Line: 1, Column: 0}, End: Location{Line: 1, Column: 4}, SourceID: 0})
	right := NewEmpty(Span{Start: Location{Line: 1, Column: 6}, End: Location{Line: 1, Column: 10}, SourceID: 0})
	binary := NewBinary(left, right, Plus, span)

	binary.Accept(visitor)

	// Should only have 1 EnterExpr call (for the binary expr itself)
	// and 1 ExitExpr call (children should be skipped)
	expectedEnterCount := 1
	expectedExitCount := 1

	if len(visitor.enterCalls) != expectedEnterCount {
		t.Errorf("Expected %d enter calls, got %d", expectedEnterCount, len(visitor.enterCalls))
	}
	if len(visitor.exitCalls) != expectedExitCount {
		t.Errorf("Expected %d exit calls, got %d", expectedExitCount, len(visitor.exitCalls))
	}
}

func TestIdentPat_Accept(t *testing.T) {
	visitor := newMockVisitor()
	span := Span{Start: Location{Line: 1, Column: 0}, End: Location{Line: 1, Column: 3}, SourceID: 0}
	pat := NewIdentPat("foo", nil, span)

	pat.Accept(visitor)

	expectedEnter := []string{"EnterPat"}
	expectedExit := []string{"ExitPat"}

	if len(visitor.enterCalls) != len(expectedEnter) {
		t.Errorf("Expected %d enter calls, got %d", len(expectedEnter), len(visitor.enterCalls))
	}
	if len(visitor.exitCalls) != len(expectedExit) {
		t.Errorf("Expected %d exit calls, got %d", len(expectedExit), len(visitor.exitCalls))
	}

	for i, expected := range expectedEnter {
		if i >= len(visitor.enterCalls) || visitor.enterCalls[i] != expected {
			t.Errorf("Expected enter call %d to be %s, got %s", i, expected, visitor.enterCalls[i])
		}
	}

	for i, expected := range expectedExit {
		if i >= len(visitor.exitCalls) || visitor.exitCalls[i] != expected {
			t.Errorf("Expected exit call %d to be %s, got %s", i, expected, visitor.exitCalls[i])
		}
	}
}

// Test that visitor interface is correctly implemented
func TestVisitorInterface(t *testing.T) {
	var _ Visitor = &DefaultVisitor{}
	var _ Visitor = newMockVisitor()
}

// Test visitor with nil arguments doesn't panic
func TestVisitorWithNilArguments(t *testing.T) {
	visitor := &DefaultVisitor{}

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Visitor methods should handle nil arguments gracefully: %v", r)
		}
	}()

	// Test Enter methods with nil
	visitor.EnterLit(nil)
	visitor.EnterPat(nil)
	visitor.EnterExpr(nil)
	visitor.EnterObjExprElem(nil)
	visitor.EnterStmt(nil)
	visitor.EnterDecl(nil)
	visitor.EnterTypeAnn(nil)
	visitor.EnterBlock(Block{Stmts: nil, Span: Span{Start: Location{Line: 0, Column: 0}, End: Location{Line: 0, Column: 0}, SourceID: 0}})

	// Test Exit methods with nil
	visitor.ExitLit(nil)
	visitor.ExitPat(nil)
	visitor.ExitExpr(nil)
	visitor.ExitObjExprElem(nil)
	visitor.ExitStmt(nil)
	visitor.ExitDecl(nil)
	visitor.ExitTypeAnn(nil)
	visitor.ExitBlock(Block{Stmts: nil, Span: Span{Start: Location{Line: 0, Column: 0}, End: Location{Line: 0, Column: 0}, SourceID: 0}})
}

// Benchmark basic visitor traversal
func BenchmarkDefaultVisitor_SimpleTraversal(b *testing.B) {
	visitor := &DefaultVisitor{}
	span := Span{Start: Location{Line: 1, Column: 0}, End: Location{Line: 1, Column: 10}, SourceID: 0}
	left := NewEmpty(Span{Start: Location{Line: 1, Column: 0}, End: Location{Line: 1, Column: 4}, SourceID: 0})
	right := NewEmpty(Span{Start: Location{Line: 1, Column: 6}, End: Location{Line: 1, Column: 10}, SourceID: 0})
	binary := NewBinary(left, right, Plus, span)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		binary.Accept(visitor)
	}
}
