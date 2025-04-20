package type_system

func (*TypeProvenance) IsProvenance() {}

type TypeProvenance struct {
	Type Type
}
