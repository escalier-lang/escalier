package ast

// TODO: implement TypeVisitor
type TypeVisitor interface {
	VisitType(t Type)
}
