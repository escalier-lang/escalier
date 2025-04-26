package type_system

type Pat interface {
	isPat()
	String() string
}

func (*IdentPat) isPat()     {}
func (*ObjectPat) isPat()    {}
func (*TuplePat) isPat()     {}
func (*ExtractorPat) isPat() {}
func (*RestPat) isPat()      {}
func (*LitPat) isPat()       {}
func (*WildcardPat) isPat()  {}

type IdentPat struct {
	Name string
	// Default      optional.Option[Expr]
}

func NewIdentPat(name string) *IdentPat {
	return &IdentPat{Name: name}
}
func (p *IdentPat) String() string {
	return p.Name
}

type ObjPatElem interface {
	isObjPatElem()
	String() string
}

func (*ObjKeyValuePat) isObjPatElem()  {}
func (*ObjShorthandPat) isObjPatElem() {}
func (*ObjRestPat) isObjPatElem()      {}

type ObjKeyValuePat struct {
	Key   string
	Value Pat
	// Default      optional.Option[Expr]
}

func NewObjKeyValuePat(key string, value Pat) *ObjKeyValuePat {
	return &ObjKeyValuePat{Key: key, Value: value}
}
func (p *ObjKeyValuePat) String() string {
	return p.Key + ": " + p.Value.String()
}

type ObjShorthandPat struct {
	Key string
	// Default optional.Option[Expr]
}

func NewObjShorthandPat(key string) *ObjShorthandPat {
	return &ObjShorthandPat{Key: key}
}
func (p *ObjShorthandPat) String() string {
	return p.Key
}

type ObjRestPat struct {
	Pattern Pat
}

func NewObjRestPat(pattern Pat) *ObjRestPat {
	return &ObjRestPat{Pattern: pattern}
}
func (p *ObjRestPat) String() string {
	return "..." + p.Pattern.String()
}

type ObjectPat struct {
	Elems []ObjPatElem
}

func NewObjectPat(elems []ObjPatElem) *ObjectPat {
	return &ObjectPat{Elems: elems}
}
func (p *ObjectPat) String() string {
	var elems []string
	for _, elem := range p.Elems {
		elems = append(elems, elem.String())
	}
	result := "{"
	for i, elem := range elems {
		if i > 0 {
			result += ", "
		}
		result += elem
	}
	result += "}"
	return result
}

type TuplePat struct {
	Elems []Pat
}

func NewTuplePat(elems []Pat) *TuplePat {
	return &TuplePat{Elems: elems}
}
func (p *TuplePat) String() string {
	var elems []string
	for _, elem := range p.Elems {
		elems = append(elems, elem.String())
	}
	result := "["
	for i, elem := range elems {
		if i > 0 {
			result += ", "
		}
		result += elem
	}
	result += "]"
	return result
}

type ExtractorPat struct {
	Name string // TODO: QualIdent
	Args []Pat
}

func NewExtractorPat(name string, args []Pat) *ExtractorPat {
	return &ExtractorPat{Name: name, Args: args}
}
func (p *ExtractorPat) String() string {
	var args []string
	for _, arg := range p.Args {
		args = append(args, arg.String())
	}
	result := p.Name + "("
	for i, arg := range args {
		if i > 0 {
			result += ", "
		}
		result += arg
	}
	result += ")"
	return result
}

type RestPat struct {
	Pattern Pat
}

func NewRestPat(pattern Pat) *RestPat {
	return &RestPat{Pattern: pattern}
}
func (p *RestPat) String() string {
	return "..." + p.Pattern.String()
}

type LitPat struct {
	Lit Lit
}

func NewLitPat(lit Lit) *LitPat {
	return &LitPat{Lit: lit}
}
func (p *LitPat) String() string {
	return p.Lit.String()
}

type WildcardPat struct{}

func NewWildcardPat() *WildcardPat {
	return &WildcardPat{}
}
func (p *WildcardPat) String() string {
	return "_"
}
