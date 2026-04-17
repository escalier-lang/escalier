package type_system

// EnterResult controls traversal after EnterType.
type EnterResult struct {
	Type         Type // nil = don't replace the node
	SkipChildren bool // true = skip child traversal, go straight to ExitType
}

type TypeVisitor interface {
	EnterType(t Type) EnterResult
	ExitType(t Type) Type
}
