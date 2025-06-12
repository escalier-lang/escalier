package ast

func (*NodeProvenance) IsProvenance() {}

type NodeProvenance struct {
	Node Node
}
