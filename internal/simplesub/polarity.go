package simplesub

// Polarity is the position a type occupies: Positive (output / covariant, e.g. a
// function's result) or Negative (input / contravariant, e.g. a parameter).
// Under algebraic subtyping a variable coalesces to the union of its lower
// bounds in Positive position and the intersection of its upper bounds in
// Negative position.
type Polarity int

const (
	Positive Polarity = iota
	Negative
)

// flip returns the opposite polarity, used when descending into contravariant
// positions such as function parameters.
func (p Polarity) flip() Polarity {
	if p == Positive {
		return Negative
	}
	return Positive
}

func (p Polarity) String() string {
	if p == Positive {
		return "positive"
	}
	return "negative"
}
