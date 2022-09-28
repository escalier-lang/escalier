use super::context::Context;
use super::util::union_many_types;
use crochet_types::{TKeyword, TLit, TObjElem, TObject, TPrim, Type};

// TODO: try to dedupe with infer_property_type()
pub fn key_of(t: &Type, ctx: &Context) -> Result<Type, String> {
    match t {
        Type::Var(_) => Err(String::from(
            "There isn't a way to infer a type from its keys",
        )),
        Type::Ref(alias) => {
            let t = ctx.lookup_alias(alias)?;
            key_of(&t, ctx)
        }
        Type::Object(TObject {
            elems,
            type_params: _,
        }) => {
            let elems: Vec<_> = elems
                .iter()
                .filter_map(|elem| match elem {
                    TObjElem::Call(_) => None,
                    TObjElem::Constructor(_) => None,
                    TObjElem::Index(_) => todo!(),
                    TObjElem::Prop(prop) => Some(Type::Lit(TLit::Str(prop.name.to_owned()))),
                })
                .collect();

            Ok(union_many_types(&elems))
        }
        Type::Prim(prim) => match prim {
            TPrim::Num => key_of(&ctx.lookup_type_and_instantiate("Number")?, ctx),
            TPrim::Bool => key_of(&ctx.lookup_type_and_instantiate("Boolean")?, ctx),
            TPrim::Str => key_of(&ctx.lookup_type_and_instantiate("String")?, ctx),
        },
        Type::Lit(lit) => match lit {
            TLit::Num(_) => key_of(&ctx.lookup_type_and_instantiate("Number")?, ctx),
            TLit::Bool(_) => key_of(&ctx.lookup_type_and_instantiate("Boolean")?, ctx),
            TLit::Str(_) => key_of(&ctx.lookup_type_and_instantiate("String")?, ctx),
        },
        Type::Tuple(tuple) => {
            let mut elems: Vec<Type> = vec![];
            for i in 0..tuple.len() {
                elems.push(Type::Lit(TLit::Num(i.to_string())))
            }
            elems.push(key_of(
                &ctx.lookup_type_and_instantiate("ReadonlyArray")?,
                ctx,
            )?);
            Ok(union_many_types(&elems))
        }
        Type::Array(_) => Ok(union_many_types(&[
            Type::Prim(TPrim::Num),
            key_of(&ctx.lookup_type_and_instantiate("ReadonlyArray")?, ctx)?,
        ])),
        Type::Lam(_) => key_of(&ctx.lookup_type_and_instantiate("Function")?, ctx),
        Type::App(_) => {
            todo!() // What does this even mean?
        }
        Type::Keyword(keyword) => match keyword {
            crochet_types::TKeyword::Null => Ok(Type::Keyword(TKeyword::Never)),
            crochet_types::TKeyword::Symbol => {
                key_of(&ctx.lookup_type_and_instantiate("Symbol")?, ctx)
            }
            crochet_types::TKeyword::Undefined => Ok(Type::Keyword(TKeyword::Never)),
            crochet_types::TKeyword::Never => Ok(Type::Keyword(TKeyword::Never)),
        },
        Type::Union(_) => todo!(),
        Type::Intersection(elems) => {
            let elems: Result<Vec<_>, String> =
                elems.iter().map(|elem| key_of(elem, ctx)).collect();
            Ok(union_many_types(&elems?))
        }
        Type::Rest(_) => {
            todo!() // What does this even mean?
        }
        Type::This => {
            todo!() // Depends on what this is referencing
        }
        Type::KeyOf(t) => key_of(&key_of(t, ctx)?, ctx),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infer;
    use crochet_parser::*;

    fn infer_prog(input: &str) -> Context {
        let prog = parse(input).unwrap();
        let mut ctx: Context = Context::default();
        infer::infer_prog(&prog, &mut ctx).unwrap()
    }

    fn get_key_of(name: &str, ctx: &Context) -> String {
        match ctx.lookup_type(name) {
            Ok(t) => {
                let t = key_of(&t, ctx).unwrap();
                format!("{t}")
            }
            Err(_) => panic!("Couldn't find type with name '{name}'"),
        }
    }

    #[test]
    fn test_object() {
        let src = r#"
        type t = {x: number, y: number};
        "#;
        let ctx = infer_prog(src);

        assert_eq!(get_key_of("t", &ctx), r#""x" | "y""#);
    }

    #[test]
    fn test_intersection() {
        let src = r#"
        type t = {a: number, b: boolean} & {b: string, c: number};
        "#;
        let ctx = infer_prog(src);

        assert_eq!(get_key_of("t", &ctx), r#""a" | "b" | "c""#);
    }

    #[test]
    fn test_number() {
        let src = r#"
        type Number = {
            toFixed: () => string,
            toString: () => string,
        };
        type t = number;
        "#;
        let ctx = infer_prog(src);

        assert_eq!(get_key_of("t", &ctx), r#""toFixed" | "toString""#);
    }

    #[test]
    fn test_string() {
        let src = r#"
        type String = {
            length: () => number,
            toLowerCase: () => string,
            toUpperCase: () => string,
        };
        type t = string;
        "#;
        let ctx = infer_prog(src);

        assert_eq!(
            get_key_of("t", &ctx),
            r#""length" | "toLowerCase" | "toUpperCase""#
        );
    }

    #[test]
    fn test_array() {
        let src = r#"
        type ReadonlyArray<T> = {
            length: number,
            map: (item: T, index: number, array: ReadonlyArray<T>) => null,
        };
        type t = number[];
        "#;
        let ctx = infer_prog(src);

        assert_eq!(get_key_of("t", &ctx), r#""length" | "map" | number"#);
    }

    #[test]
    fn test_tuple() {
        let src = r#"
        type ReadonlyArray<T> = {
            length: number,
            map: (item: T, index: number, array: ReadonlyArray<T>) => null,
        };
        type t = [1, 2, 3];
        "#;
        let ctx = infer_prog(src);

        assert_eq!(get_key_of("t", &ctx), r#""length" | "map" | 0 | 1 | 2"#);
    }

    #[test]
    fn test_function() {
        let src = r#"
        type Function = {
            call: () => null,
            apply: () => null,
            bind: () => null,
        };
        type t = () => boolean;
        "#;
        let ctx = infer_prog(src);

        assert_eq!(get_key_of("t", &ctx), r#""apply" | "bind" | "call""#);
    }
}