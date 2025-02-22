package parser

// TODO: use an interface for the stack
type Stack[T any] []T

func NewStack[T any]() Stack[T] {
	return make(Stack[T], 0)
}

func (s *Stack[T]) Push(value T) {
	*s = append(*s, value)
}

func (s *Stack[T]) Pop() T {
	index := len(*s) - 1
	value := (*s)[index]
	*s = (*s)[:index]
	return value
}

func (s *Stack[T]) Peek() T {
	return (*s)[len(*s)-1]
}

func (s *Stack[T]) IsEmpty() bool {
	return len(*s) == 0
}
