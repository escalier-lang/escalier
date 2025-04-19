package checker

type Checker struct{}

func NewChecker() *Checker {
	return &Checker{}
}

type Context struct {
	Filename   string
	Scope      *Scope
	IsAsync    bool
	IsPatMatch bool
}
