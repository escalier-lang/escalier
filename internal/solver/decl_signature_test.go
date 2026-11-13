package solver

import (
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/stretchr/testify/require"
)

// renderWritten joins the parts PrintWritten returns as `<binders>(receiver, params) -> ret
// throws T`, so a test asserts them in one string.
func renderWritten(sig DeclSignature) string {
	parts := sig.PrintWritten()
	var b strings.Builder
	if len(parts.Binders) > 0 {
		b.WriteString("<" + strings.Join(parts.Binders, ", ") + ">")
	}
	params := parts.Params
	if parts.Receiver != "" {
		params = append([]string{parts.Receiver}, params...)
	}
	b.WriteString("(" + strings.Join(params, ", ") + ") -> " + parts.Ret)
	if parts.Throws != "" {
		b.WriteString(" throws " + parts.Throws)
	}
	return b.String()
}

// inferForSignatures infers src as a module and returns its Info with the declarations of
// its root namespace.
func inferForSignatures(t *testing.T, src string) (*Info, []ast.Decl) {
	t.Helper()
	module := parseModuleFiles(t, map[string]string{"input.esc": src})
	c := newTestChecker()
	scope := c.preludeScope().Child()
	c.inferDepGraph(scope, 0, module, dep_graph.BuildDepGraph(module))
	require.Empty(t, c.errs)
	ns, ok := module.Namespaces.Get("")
	require.True(t, ok)
	return c.info, ns.Decls
}

// TestFuncDeclSignaturePrintWritten covers which lifetimes PrintWritten leaves for elision
// to assign. Each case's source declares one function, f.
func TestFuncDeclSignaturePrintWritten(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		expected string
	}{
		{
			name:     "OneInputLifetime",
			src:      `fn f(p) { return p.x }`,
			expected: "<T0>(&{x: T0}) -> &T0",
		},
		{
			name:     "ReturnedBorrowOfOneOfTwo",
			src:      "type Flat = {x: number}\nfn f(a: &Flat, b: &Flat) -> &Flat { return a }",
			expected: "<'a>(&'a Flat, &Flat) -> &'a Flat",
		},
		{
			name:     "LifetimeNotInOutput",
			src:      "type Flat = {x: number}\nfn f(a: &Flat, b: &Flat) -> number { return a.x + b.x }",
			expected: "(&Flat, &Flat) -> number",
		},
		{
			name: "WrittenLifetimeElided",
			src:  "type Flat = {x: number}\nfn f<'q>(a: &'q Flat) -> &'q Flat { return a }",
			// The lifetime the source names reaches the scheme as a free variable, so it is
			// elided like an inferred one.
			expected: "(&Flat) -> &Flat",
		},
		{
			name: "LifetimeInsideFuncParam",
			src:  "type Flat = {x: number}\nfn f(a: &Flat, g: fn (x: &Flat) -> number) -> number { return g(a) }",
			// The call ties x to a, so the lifetime occurs inside g's type and stays written.
			expected: "<'a>(&'a Flat, fn (x: &'a Flat) -> number) -> number",
		},
		{
			name:     "TypeParamBound",
			src:      "fn f<T: {x: number}>(p: &T) -> number { return p.x }",
			expected: "<T: {x: number}>(&T) -> number",
		},
		{
			name:     "OutlivesBound",
			src:      "type Flat = {x: number}\nfn f<'a, 'b: 'a>(p: &'a Flat, q: &'b Flat) -> &'a Flat { return if p.x > 0 { p } else { q } }",
			expected: "<'a, 'b: 'a>(&'a Flat, &'b Flat) -> &'a Flat",
		},
		{
			name:     "StaticBorrow",
			src:      "type Flat = {x: number}\nfn f(p: &'static Flat) -> &'static Flat { return p }",
			expected: "(&'static Flat) -> &'static Flat",
		},
		{
			name:     "ClassLifetimeArgument",
			src:      "type Flat = {x: number}\nclass Ref<'a> { v: &'a Flat }\nfn f<'a>(r: Ref<'a>) -> &'a Flat { return r.v }",
			expected: "<'a>(Ref<'a>) -> &'a Flat",
		},
		{
			name:     "AliasLifetimeArgument",
			src:      "type Flat = {x: number}\ntype R<'a> = {v: &'a Flat}\nfn f<'a>(r: R<'a>) -> &'a Flat { return r.v }",
			expected: "<'a>(R<'a>) -> &'a Flat",
		},
		{
			name:     "TypeParamDefault",
			src:      "fn f<T = number>(p: &T) -> number { return 1 }",
			expected: "<T = number>(&T) -> number",
		},
		{
			name:     "FuncParamWithLifetimeBinders",
			src:      "type Flat = {x: number}\nfn f(g: fn <'a, 'b: 'a>(x: &'a Flat, y: &'b Flat) -> &'a Flat) -> number { return 1 }",
			expected: "(fn <'a>(x: &'a Flat, y: &'a Flat) -> &'a Flat) -> number",
		},
		{
			name:     "ThrowsType",
			src:      "fn f(x: number) -> number throws string { if x > 0 { throw \"neg\" } return x }",
			expected: "(number) -> number throws string",
		},
		{
			name:     "ThrowsBorrow",
			src:      "type Flat = {x: number}\nfn f(a: &Flat, b: &Flat) -> number throws &Flat { if a.x > 0 { throw a } return b.x }",
			expected: "<'a>(&'a Flat, &Flat) -> number throws &'a Flat",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, decls := inferForSignatures(t, tt.src)
			var fn *ast.FuncDecl
			for _, d := range decls {
				if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "f" {
					fn = fd
				}
			}
			require.NotNil(t, fn)
			sig, ok := info.FuncDeclSignature(fn)
			require.True(t, ok)
			require.Equal(t, tt.expected, renderWritten(sig))
		})
	}
}

// TestMethodSignature covers which signature MethodSignature returns for each method of a
// class, by the method's position in the class body. A method with no signature expects "".
func TestMethodSignature(t *testing.T) {
	src := `
		type Flat = {x: number}
		class Box {
			inner: Flat,
			peek(&self, other: &Flat) -> &Flat { return &self.inner },
			get(&self) { return self.inner.x },
			reader(&self) -> fn () -> number { return fn () { return self.inner.x } },
			at(&self, i: number) -> number { return i },
			at(&self, s: string) -> string { return s },
			static make(x: number) { return x + 1 },
		}
	`
	info, decls := inferForSignatures(t, src)
	var class *ast.ClassDecl
	for _, d := range decls {
		if cd, ok := d.(*ast.ClassDecl); ok {
			class = cd
		}
	}
	require.NotNil(t, class)
	var got []string
	for _, elem := range class.Body {
		m, ok := elem.(*ast.MethodElem)
		if !ok {
			continue
		}
		sig, ok := info.MethodSignature(class, m)
		require.True(t, ok)
		got = append(got, renderWritten(sig))
	}
	require.Equal(t, []string{
		"(&self, &Flat) -> &Flat",
		"(&self) -> number",
		"(&self) -> fn () -> number",
		"(&self, number) -> number",
		"(&self, string) -> string",
		"(number) -> number",
	}, got)
}

// TestMethodSignatureComputedKey asserts that MethodSignature returns no signature when a
// method on the same side of the class has a computed key, whose name only the checker
// resolves.
func TestMethodSignatureComputedKey(t *testing.T) {
	src := `
		val K = "k"
		class C {
			[K](&self) -> number { return 1 },
			get(&self) { return 2 },
		}
	`
	info, decls := inferForSignatures(t, src)
	for _, d := range decls {
		class, ok := d.(*ast.ClassDecl)
		if !ok {
			continue
		}
		m := class.Body[1].(*ast.MethodElem)
		_, ok = info.MethodSignature(class, m)
		require.False(t, ok)
	}
}

// TestMethodSignatureOwnComputedKey asserts that MethodSignature returns no signature for a
// method whose own key is computed.
func TestMethodSignatureOwnComputedKey(t *testing.T) {
	src := `
		val K = "k"
		class C {
			[K](&self) -> number { return 1 },
		}
	`
	info, decls := inferForSignatures(t, src)
	var class *ast.ClassDecl
	for _, d := range decls {
		if cd, ok := d.(*ast.ClassDecl); ok {
			class = cd
		}
	}
	require.NotNil(t, class)
	_, ok := info.MethodSignature(class, class.Body[0].(*ast.MethodElem))
	require.False(t, ok)
}

// TestSignaturesWithoutRecordedTypes asserts that FuncDeclSignature and MethodSignature
// return no signature from a table built by NewInfo, which records no types.
func TestSignaturesWithoutRecordedTypes(t *testing.T) {
	_, decls := inferForSignatures(t, "fn f(x: number) -> number { return x }\nclass C { get(&self) -> number { return 1 } }")
	info := NewInfo()
	for _, d := range decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			_, ok := info.FuncDeclSignature(d)
			require.False(t, ok)
		case *ast.ClassDecl:
			_, ok := info.MethodSignature(d, d.Body[0].(*ast.MethodElem))
			require.False(t, ok)
		}
	}
}

// TestMethodSignatureOwnLifetime asserts that a method keeps a lifetime it declares when
// elision would assign the receiver's lifetime instead.
func TestMethodSignatureOwnLifetime(t *testing.T) {
	src := `
		type Flat = {x: number}
		class Box {
			inner: Flat,
			pick<'a>(&self, a: &'a Flat, b: &Flat) -> &'a Flat { return a },
		}
	`
	info, decls := inferForSignatures(t, src)
	var class *ast.ClassDecl
	for _, d := range decls {
		if cd, ok := d.(*ast.ClassDecl); ok {
			class = cd
		}
	}
	require.NotNil(t, class)
	sig, ok := info.MethodSignature(class, class.Body[1].(*ast.MethodElem))
	require.True(t, ok)
	require.Equal(t, "<'a>(&self, &'a Flat, &Flat) -> &'a Flat", renderWritten(sig))
}
