package type_system

type TypeVisitor interface {
	EnterType(t Type)
	ExitType(t Type) Type
}
