package type_system

type TypeVisitor interface {
	VisitType(t Type)
}
