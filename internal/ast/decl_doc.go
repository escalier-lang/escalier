package ast

// Doc and SetDoc implementations for every declaration kind. A
// declaration's doc is the leading JSDoc (`/** ... */`) block the parser
// found immediately above it, stored verbatim with its delimiters. The
// printer re-emits it above the declaration, which is what lets a
// generated `.esc` file survive a parse-and-reprint unchanged.

func (d *VarDecl) Doc() string       { return d.doc }
func (d *VarDecl) SetDoc(doc string) { d.doc = doc }

func (d *FuncDecl) Doc() string       { return d.doc }
func (d *FuncDecl) SetDoc(doc string) { d.doc = doc }

func (d *TypeDecl) Doc() string       { return d.doc }
func (d *TypeDecl) SetDoc(doc string) { d.doc = doc }

func (d *InterfaceDecl) Doc() string       { return d.doc }
func (d *InterfaceDecl) SetDoc(doc string) { d.doc = doc }

func (d *EnumDecl) Doc() string       { return d.doc }
func (d *EnumDecl) SetDoc(doc string) { d.doc = doc }

func (d *ClassDecl) Doc() string       { return d.doc }
func (d *ClassDecl) SetDoc(doc string) { d.doc = doc }

func (e *ExportAssignmentStmt) Doc() string       { return e.doc }
func (e *ExportAssignmentStmt) SetDoc(doc string) { e.doc = doc }

func (d *DeclareModuleDecl) Doc() string       { return d.doc }
func (d *DeclareModuleDecl) SetDoc(doc string) { d.doc = doc }

func (d *DeclareGlobalDecl) Doc() string       { return d.doc }
func (d *DeclareGlobalDecl) SetDoc(doc string) { d.doc = doc }

func (d *NamespaceDecl) Doc() string       { return d.doc }
func (d *NamespaceDecl) SetDoc(doc string) { d.doc = doc }
