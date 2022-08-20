use itertools::{join, Itertools};
use std::fmt;
use std::hash::Hash;

use crochet_ast::literal::Lit as AstLit;

use crate::{Lit, Primitive};

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct TProp {
    pub name: String,
    pub optional: bool,
    pub mutable: bool,
    pub ty: Type,
}

impl TProp {
    pub fn get_type(&self) -> Type {
        match self.optional {
            true => Type::Union(vec![self.ty.to_owned(), Type::Prim(Primitive::Undefined)]),
            false => self.ty.to_owned(),
        }
    }
}

impl fmt::Display for TProp {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        let Self {
            name,
            optional,
            mutable,
            ty,
        } = self;
        match (optional, mutable) {
            (false, false) => write!(f, "{name}: {ty}"),
            (true, false) => write!(f, "{name}?: {ty}"),
            (false, true) => write!(f, "mut {name}: {ty}"),
            (true, true) => write!(f, "mut {name}?: {ty}"),
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct AppType {
    pub args: Vec<Type>,
    pub ret: Box<Type>,
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct TFnParam {
    pub pat: TPat,
    pub ty: Type,
    pub optional: bool,
}

impl TFnParam {
    pub fn get_type(&self) -> Type {
        match self.optional {
            true => Type::Union(vec![self.ty.to_owned(), Type::Prim(Primitive::Undefined)]),
            false => self.ty.to_owned(),
        }
    }
    pub fn get_name(&self, index: &usize) -> String {
        match &self.pat {
            TPat::Ident(bi) => bi.name.to_owned(),
            _ => format!("arg{index}"),
        }
    }
}

impl fmt::Display for TFnParam {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        let Self { pat, ty, optional } = self;
        match optional {
            true => write!(f, "{pat}?: {ty}"),
            false => write!(f, "{pat}: {ty}"),
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub enum TPat {
    Ident(BindingIdent),
    Rest(RestPat),
    Array(ArrayPat),
    Object(TObjectPat),
}

impl fmt::Display for TPat {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        match self {
            TPat::Ident(bi) => write!(f, "{bi}"),
            TPat::Rest(rest) => write!(f, "{rest}"),
            TPat::Array(array) => write!(f, "{array}"),
            TPat::Object(obj) => write!(f, "{obj}"),
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct BindingIdent {
    pub name: String,
    pub mutable: bool,
}

impl fmt::Display for BindingIdent {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        let Self { name, mutable } = self;
        match mutable {
            false => write!(f, "{name}"),
            true => write!(f, "mut {name}"),
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct RestPat {
    pub arg: Box<TPat>,
}

impl fmt::Display for RestPat {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        let Self { arg } = self;
        write!(f, "...{arg}")
    }
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct ArrayPat {
    pub elems: Vec<Option<TPat>>,
}

impl fmt::Display for ArrayPat {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        let Self { elems } = self;
        let elems = elems.iter().map(|elem| match elem {
            Some(elem) => format!("{elem}"),
            None => String::from(" "),
        });
        write!(f, "[{}]", join(elems, ", "))
    }
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct TObjectPat {
    pub props: Vec<TObjectPatProp>,
}

impl fmt::Display for TObjectPat {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        let Self { props } = self;
        write!(f, "{{{}}}", join(props, ", "))
    }
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub enum TObjectPatProp {
    KeyValue(TObjectKeyValuePatProp),
    Assign(TObjectAssignPatProp),
    Rest(RestPat),
}

impl fmt::Display for TObjectPatProp {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        match self {
            TObjectPatProp::KeyValue(kv) => write!(f, "{kv}"),
            TObjectPatProp::Assign(assign) => write!(f, "{assign}"),
            TObjectPatProp::Rest(rest) => write!(f, "{rest}"),
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct TObjectKeyValuePatProp {
    pub key: String,
    pub value: TPat,
}

impl fmt::Display for TObjectKeyValuePatProp {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        let Self { key, value } = self;
        write!(f, "{key}: {value}")
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Hash)]
pub struct TObjectAssignPatProp {
    pub key: String,
    pub value: Option<Type>,
}

impl fmt::Display for TObjectAssignPatProp {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        let Self { key, value } = self;
        match value {
            Some(value) => write!(f, "{key} = {value}"),
            None => write!(f, "{key}"),
        }
    }
}

#[derive(Clone, Debug, Eq)]
pub struct LamType {
    pub params: Vec<TFnParam>,
    pub ret: Box<Type>,
}

impl PartialEq for LamType {
    fn eq(&self, other: &Self) -> bool {
        self.params == other.params && self.ret == other.ret
    }
}

impl Hash for LamType {
    fn hash<H: std::hash::Hasher>(&self, state: &mut H) {
        self.params.hash(state);
        self.ret.hash(state);
    }
}

#[derive(Clone, Debug, Eq)]
pub struct AliasType {
    pub name: String,
    pub type_params: Option<Vec<Type>>,
}

impl PartialEq for AliasType {
    fn eq(&self, other: &Self) -> bool {
        self.name == other.name && self.type_params == other.type_params
    }
}

impl Hash for AliasType {
    fn hash<H: std::hash::Hasher>(&self, state: &mut H) {
        self.name.hash(state);
        self.type_params.hash(state);
    }
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub enum Type {
    Var(i32), // i32 is the if of the type variable
    App(AppType),
    Lam(LamType),
    Wildcard,
    // Query, // use for typed holes
    Prim(Primitive),
    Lit(Lit),
    Union(Vec<Type>),
    Intersection(Vec<Type>),
    Object(Vec<TProp>),
    Alias(AliasType),
    Tuple(Vec<Type>),
    Array(Box<Type>),
    Rest(Box<Type>), // TODO: rename this to Spread
}

impl From<AstLit> for Type {
    fn from(ast_lit: AstLit) -> Self {
        Type::Lit(match ast_lit {
            AstLit::Num(n) => Lit::Num(n.value),
            AstLit::Bool(b) => Lit::Bool(b.value),
            AstLit::Str(s) => Lit::Str(s.value),
            AstLit::Null(_) => Lit::Null,
            AstLit::Undefined(_) => Lit::Undefined,
        })
    }
}

impl From<Lit> for Type {
    fn from(lit: Lit) -> Self {
        Type::Lit(lit)
    }
}

impl fmt::Display for Type {
    // TODO: add in parentheses where necessary to get the precedence right
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        match &self {
            Type::Var(id) => write!(f, "t{id}"),
            Type::App(AppType { args, ret }) => {
                write!(f, "({}) => {}", join(args, ", "), ret)
            }
            Type::Lam(LamType { params, ret, .. }) => {
                write!(f, "({}) => {}", join(params, ", "), ret)
            }
            Type::Wildcard => write!(f, "_"),
            Type::Prim(prim) => write!(f, "{}", prim),
            Type::Lit(lit) => write!(f, "{}", lit),
            Type::Union(types) => {
                let strings: Vec<_> = types.iter().map(|t| format!("{t}")).sorted().collect();
                write!(f, "{}", join(strings, " | "))
            }
            Type::Intersection(types) => {
                let strings: Vec<_> = types.iter().map(|t| format!("{t}")).sorted().collect();
                write!(f, "{}", join(strings, " & "))
            }
            Type::Object(props) => write!(f, "{{{}}}", join(props, ", ")),
            Type::Alias(AliasType {
                name, type_params, ..
            }) => match type_params {
                Some(params) => write!(f, "{name}<{}>", join(params, ", ")),
                None => write!(f, "{name}"),
            },
            Type::Tuple(types) => write!(f, "[{}]", join(types, ", ")),
            Type::Array(t) => write!(f, "{t}[]"),
            Type::Rest(arg) => write!(f, "...{arg}"),
        }
    }
}