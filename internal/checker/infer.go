package checker

import (
	"fmt"
	"slices"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

func (c *Checker) InferScript(ctx Context, m *ast.Script) (*Scope, []Error) {
	errors := []Error{}
	ctx = ctx.WithNewScope()

	for _, stmt := range m.Stmts {
		switch stmt := stmt.(type) {
		case *ast.DeclStmt:
			declErrors := c.inferDecl(ctx, stmt.Decl)
			errors = slices.Concat(errors, declErrors)
		case *ast.ExprStmt:
			_, exprErrors := c.inferExpr(ctx, stmt.Expr)
			errors = slices.Concat(errors, exprErrors)
		case *ast.ReturnStmt:
			panic("TODO: infer return statement")
		}
	}

	return ctx.Scope, errors
}

type QualifiedIdent string

// NewQualifiedIdent creates a QualifiedIdent from a slice of string parts
func NewQualifiedIdent(parts []string) QualifiedIdent {
	return QualifiedIdent(strings.Join(parts, "."))
}

// Parts returns the QualifiedIdent as a slice of string parts
func (qi QualifiedIdent) Parts() []string {
	if qi == "" {
		return []string{}
	}
	return strings.Split(string(qi), ".")
}

// AccessKey represents either a property name or an index for accessing object/array elements
type AccessKey interface {
	isAccessKey()
}

type PropertyKey struct {
	Name     string
	OptChain bool
	Span     ast.Span
}

func (pk PropertyKey) isAccessKey() {}

type IndexKey struct {
	Type type_system.Type
	Span ast.Span
}

func (ik IndexKey) isAccessKey() {}

// getSpanFromAccessKey extracts the span from an AccessKey
func getSpanFromAccessKey(key AccessKey) ast.Span {
	switch k := key.(type) {
	case PropertyKey:
		return k.Span
	case IndexKey:
		return k.Span
	default:
		return DEFAULT_SPAN
	}
}

func (c *Checker) astKeyToTypeKey(ctx Context, key ast.ObjKey) (*type_system.ObjTypeKey, []Error) {
	switch key := key.(type) {
	case *ast.IdentExpr:
		newKey := type_system.NewStrKey(key.Name)
		return &newKey, nil
	case *ast.StrLit:
		newKey := type_system.NewStrKey(key.Value)
		return &newKey, nil
	case *ast.NumLit:
		newKey := type_system.NewNumKey(key.Value)
		return &newKey, nil
	case *ast.ComputedKey:
		// TODO: return the error
		keyType, _ := c.inferExpr(ctx, key.Expr) // infer the expression for side-effects

		switch t := type_system.Prune(keyType).(type) {
		case *type_system.LitType:
			switch lit := t.Lit.(type) {
			case *type_system.StrLit:
				newKey := type_system.NewStrKey(lit.Value)
				return &newKey, nil
			case *type_system.NumLit:
				newKey := type_system.NewNumKey(lit.Value)
				return &newKey, nil
			default:
				return nil, []Error{&InvalidObjectKeyError{Key: t, span: key.Span()}}
			}
		case *type_system.UniqueSymbolType:
			newKey := type_system.NewSymKey(t.Value)
			return &newKey, nil
		default:
			panic(&InvalidObjectKeyError{Key: t, span: key.Span()})
		}
	default:
		panic(fmt.Sprintf("Unknown object key type: %T", key))
	}
}

// inferBlock infers the types of all statements in a block and returns the type
// of the block. The type of the block is the type of the last statement if it's
// an expression statement, otherwise it returns the provided default type.
func (c *Checker) inferBlock(
	ctx Context,
	block *ast.Block,
	defaultType type_system.Type,
) (type_system.Type, []Error) {
	errors := []Error{}

	// Process all statements in the block
	for _, stmt := range block.Stmts {
		stmtErrors := c.inferStmt(ctx, stmt)
		errors = slices.Concat(errors, stmtErrors)
	}

	// The type of the block is the type of the last statement if it's an expression
	resultType := defaultType
	if len(block.Stmts) > 0 {
		lastStmt := block.Stmts[len(block.Stmts)-1]
		if exprStmt, ok := lastStmt.(*ast.ExprStmt); ok {
			if inferredType := exprStmt.Expr.InferredType(); inferredType != nil {
				resultType = inferredType
			}
		}
	}

	return resultType, errors
}

func (c *Checker) inferStmt(ctx Context, stmt ast.Stmt) []Error {
	switch stmt := stmt.(type) {
	case *ast.ExprStmt:
		_, errors := c.inferExpr(ctx, stmt.Expr)
		return errors
	case *ast.DeclStmt:
		return c.inferDecl(ctx, stmt.Decl)
	case *ast.ReturnStmt:
		errors := []Error{}
		if stmt.Expr != nil {
			// The inferred type is ignored here, but inferExpr still attaches
			// the inferred type to the expression.  This is used later on this
			// file, search for `ReturnVisitor` to see how it is used.
			_, exprErrors := c.inferExpr(ctx, stmt.Expr)
			errors = exprErrors
		}
		return errors
	default:
		panic(fmt.Sprintf("Unknown statement type: %T", stmt))
	}
}

// createTypeParamSubstitutions creates a map of type parameter substitutions from type arguments and type parameters,
// handling default values when type arguments are nil.
func createTypeParamSubstitutions(typeArgs []type_system.Type, typeParams []*type_system.TypeParam) map[string]type_system.Type {
	substitutions := make(map[string]type_system.Type, len(typeArgs))
	for typeArg, param := range Zip(typeArgs, typeParams) {
		if param.Default != nil && typeArg == nil {
			// Use the default type if the type argument is nil
			substitutions[param.Name] = param.Default
		} else {
			substitutions[param.Name] = typeArg
		}
	}
	return substitutions
}

// resolveQualifiedTypeAliasFromString resolves a qualified type name from a string representation
func (c *Checker) resolveQualifiedTypeAliasFromString(ctx Context, qualifiedName string) *type_system.TypeAlias {
	// Simple case: no dots, just a regular identifier
	if !strings.Contains(qualifiedName, ".") {
		return ctx.Scope.getTypeAlias(qualifiedName)
	}

	// Split the qualified name and traverse namespaces
	parts := strings.Split(qualifiedName, ".")
	if len(parts) < 2 {
		return ctx.Scope.getTypeAlias(qualifiedName)
	}

	// Start from the current scope and traverse through namespaces
	// We use .getNamespace() here since it'll look through the current scope
	// and any parent scopes as needed.
	namespace := ctx.Scope.getNamespace(parts[0])

	// Navigate through all but the last part (which is the type name)
	for _, part := range parts[1 : len(parts)-1] {
		namespace = namespace.Namespaces[part]
	}

	// Look for the type in the final namespace using the proper scope method
	typeName := parts[len(parts)-1]
	return namespace.Types[typeName]
}

// resolveQualifiedTypeAlias resolves a qualified type name by traversing namespace hierarchy
func (c *Checker) resolveQualifiedTypeAlias(ctx Context, qualIdent ast.QualIdent) *type_system.TypeAlias {
	switch qi := qualIdent.(type) {
	case *ast.Ident:
		// Simple identifier, use existing scope lookup
		return ctx.Scope.getTypeAlias(qi.Name)
	case *ast.Member:
		// Qualified identifier like A.B.Type
		// First resolve the left part (A.B)
		leftNamespace := c.resolveQualifiedNamespace(ctx, qi.Left)
		if leftNamespace == nil {
			return nil
		}
		// Then look for the type in the resolved namespace
		if typeAlias, ok := leftNamespace.Types[qi.Right.Name]; ok {
			return typeAlias
		}
		return nil
	default:
		return nil
	}
}

func (c *Checker) resolveQualifiedValue(ctx Context, qualIdent ast.QualIdent) *type_system.Binding {
	switch qi := qualIdent.(type) {
	case *ast.Ident:
		// Simple identifier, use existing scope lookup
		return ctx.Scope.GetValue(qi.Name)
	case *ast.Member:
		// Qualified identifier like A.B.Type
		// First resolve the left part (A.B)
		leftNamespace := c.resolveQualifiedNamespace(ctx, qi.Left)
		if leftNamespace == nil {
			return nil
		}
		// Then look for the type in the resolved namespace
		if binding, ok := leftNamespace.Values[qi.Right.Name]; ok {
			return binding
		}
		return nil
	default:
		return nil
	}
}

// resolveQualifiedNamespace resolves a qualified identifier to a namespace
func (c *Checker) resolveQualifiedNamespace(ctx Context, qualIdent ast.QualIdent) *type_system.Namespace {
	switch qi := qualIdent.(type) {
	case *ast.Ident:
		// Simple identifier, check if it's a namespace
		return ctx.Scope.getNamespace(qi.Name)
	case *ast.Member:
		// Qualified identifier like A.B
		// First resolve the left part
		leftNamespace := c.resolveQualifiedNamespace(ctx, qi.Left)
		if leftNamespace == nil {
			return nil
		}
		// Then look for the right part in the resolved namespace
		if namespace, ok := leftNamespace.Namespaces[qi.Right.Name]; ok {
			return namespace
		}
		return nil
	default:
		return nil
	}
}

func convertQualIdent(astIdent ast.QualIdent) type_system.QualIdent {
	switch id := astIdent.(type) {
	case *ast.Ident:
		return type_system.NewIdent(id.Name)
	case *ast.Member:
		left := convertQualIdent(id.Left)
		right := type_system.NewIdent(id.Right.Name)
		return &type_system.Member{Left: left, Right: right}
	default:
		panic(fmt.Sprintf("Unknown QualIdent type: %T", astIdent))
	}
}

// generateSubstitutionSets creates substitution maps for type parameters and type arguments,
// handling cartesian products when union types are present in the type arguments.
func (c *Checker) generateSubstitutionSets(
	ctx Context,
	typeParams []*type_system.TypeParam,
	typeArgs []type_system.Type,
) ([]map[string]type_system.Type, []Error) {
	// If no type params or args, return empty slice
	if len(typeParams) == 0 || len(typeArgs) == 0 {
		return []map[string]type_system.Type{}, nil
	}

	var errors []Error

	// Extract all possible types for each type argument position
	argTypeSets := make([][]type_system.Type, len(typeArgs))
	for i, argType := range typeArgs {
		// TODO: recursively expand union types in case some of the elements are
		// also union types.
		argType, argErrors := c.ExpandType(ctx, argType, 1)
		if len(argErrors) > 0 {
			errors = append(errors, argErrors...)
		}
		if unionType, ok := argType.(*type_system.UnionType); ok {
			// For union types, use all the union members
			argTypeSets[i] = unionType.Types
		} else {
			// For non-union types, create a single-element slice
			argTypeSets[i] = []type_system.Type{argType}
		}
	}

	// Generate cartesian product
	var result []map[string]type_system.Type

	// Helper function to generate cartesian product recursively
	var generateCombinations func(int, map[string]type_system.Type)
	generateCombinations = func(pos int, current map[string]type_system.Type) {
		if pos >= len(typeParams) {
			// Make a copy of the current map and add it to results
			combination := make(map[string]type_system.Type)
			for k, v := range current {
				combination[k] = v
			}
			result = append(result, combination)
			return
		}

		// Get the type parameter name for this position
		typeParamName := typeParams[pos].Name

		// Try each possible type for this position
		for _, argType := range argTypeSets[pos] {
			current[typeParamName] = argType
			generateCombinations(pos+1, current)
		}
	}

	generateCombinations(0, make(map[string]type_system.Type))

	return result, errors
}

// isMutableType checks if a type allows mutation
func (c *Checker) isMutableType(t type_system.Type) bool {
	t = type_system.Prune(t)

	switch t.(type) {
	case *type_system.MutabilityType:
		return true
	default:
		return false
	}
}
