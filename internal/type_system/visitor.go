package type_system

type TypeVisitor interface {
	EnterType(t Type) Type
	ExitType(t Type) Type
}
