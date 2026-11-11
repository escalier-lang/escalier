package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// funcElision is the lifetime a function type written with no lifetime gets, as `fn () -> R`
// does where `'a fn () -> R` writes one. It depends on where the type is written, the way an
// elided `&` borrow's lifetime does.
type funcElision int

const (
	// elideStatic gives the function type no lifetime, so it holds no borrow that ends before
	// 'static. It applies outside function signatures and bodies: in a module-level binding's
	// annotation, a class field, an interface, a type alias, and the fields and elements inside
	// those.
	elideStatic funcElision = iota
	// elideFresh gives the function type a fresh lifetime, the way an elided `&` gets one. It
	// applies in a parameter, in a return type whose signature has an input lifetime, and in
	// an annotation inside a body.
	elideFresh
)

// elideFuncsAs makes mode the elision of a function type written with no lifetime, and returns
// a function that restores the elision in force before the call.
func (c *checker) elideFuncsAs(mode funcElision) (restore func()) {
	saved := c.fnElision
	c.fnElision = mode
	return func() { c.fnElision = saved }
}

// elidedFuncLifetime returns the lifetime of a function type written with no lifetime at the
// current position. It is nil, which reads as 'static, under elideStatic, and a fresh lifetime
// at lvl under elideFresh.
func (c *checker) elidedFuncLifetime(lvl int) soltype.Lifetime {
	if c.fnElision == elideFresh {
		return c.ctx.freshLifetime(lvl)
	}
	return nil
}

// returnElision returns the elision of a signature's return type. inputMark is the lifetime
// counter before the signature's receiver and parameters resolved, and lifetimeParams is the
// signature's `<'a>` list. The return type takes elideFresh when the signature has an input
// lifetime, which is a lifetime its receiver or parameters minted or one it declares, so a
// function it returns may hold what the input borrows. It takes elideStatic otherwise, since
// a function returned from a signature with no input lifetime has nothing to borrow from.
func (c *checker) returnElision(inputMark int, lifetimeParams []*ast.LifetimeParam) funcElision {
	if c.ctx.lifetimeCounter > inputMark || len(lifetimeParams) > 0 {
		return elideFresh
	}
	return elideStatic
}

// noteSignatureLifetimes maps the variable each of params names in the current named-lifetime
// scope to its name in signatureLts. params is the `<'a>` list of a function with a body, whose
// lifetimes the rigid-lifetime checks hold to the bounds the list declares.
func (c *checker) noteSignatureLifetimes(params []*ast.LifetimeParam) {
	for _, p := range params {
		lt, found := c.namedLifetimes[p.Name]
		if !found {
			continue
		}
		if c.signatureLts == nil {
			c.signatureLts = map[*soltype.LifetimeVar]string{}
		}
		c.signatureLts[lt] = "'" + p.Name
	}
}

// isStaticFuncLt reports whether lt, a function type's lifetime, is 'static. A nil lifetime
// reads as 'static, since a function with none holds no borrow.
func isStaticFuncLt(lt soltype.Lifetime) bool {
	if lt == nil {
		return true
	}
	_, static := lt.(*soltype.StaticLifetime)
	return static
}
