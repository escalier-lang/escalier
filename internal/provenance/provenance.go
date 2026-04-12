package provenance

import "strconv"

type Provenance interface{ IsProvenance() }

// SpanProvenance records a source location (line:column-line:column) without
// importing the ast package.  Used by PropertyElem.InferredAt to remember where
// a property was first inferred during row-type inference.
type SpanProvenance struct {
	StartLine   int
	StartColumn int
	EndLine     int
	EndColumn   int
}

func (*SpanProvenance) IsProvenance() {}

func (s *SpanProvenance) String() string {
	return strconv.Itoa(s.StartLine) + ":" + strconv.Itoa(s.StartColumn) +
		"-" + strconv.Itoa(s.EndLine) + ":" + strconv.Itoa(s.EndColumn)
}
