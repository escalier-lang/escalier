use escalier_ast::*;

use crate::parse_error::ParseError;
use crate::parser::{IdentMode, Parser};
use crate::token::*;

impl<'a> Parser<'a> {
    pub fn parse_pattern(&mut self) -> Result<Pattern, ParseError> {
        let mut span = self.peek().unwrap_or(&EOF).span;
        let kind = match self.next().unwrap_or(EOF.clone()).kind {
            TokenKind::Identifier(name) => {
                match self.peek().unwrap_or(&EOF).kind {
                    TokenKind::Is => {
                        self.next(); // consumes 'is'
                        let next = self.next().unwrap_or(EOF.clone());
                        let is_id = match &next.kind {
                            TokenKind::Identifier(name) => Ident {
                                name: name.to_owned(),
                                span: next.span,
                            },
                            TokenKind::Number => Ident {
                                name: "number".to_string(),
                                span: next.span,
                            },
                            TokenKind::String => Ident {
                                name: "string".to_string(),
                                span: next.span,
                            },
                            TokenKind::Boolean => Ident {
                                name: "boolean".to_string(),
                                span: next.span,
                            },
                            TokenKind::Symbol => Ident {
                                name: "symbol".to_string(),
                                span: next.span,
                            },
                            _ => panic!("expected identifier after 'is'"),
                        };
                        PatternKind::Is(IsPat {
                            ident: BindingIdent {
                                name,
                                span,
                                mutable: false,
                            },
                            is_id,
                        })
                    }
                    _ => PatternKind::Ident(BindingIdent {
                        name,
                        span,
                        mutable: false,
                    }),
                }
            }
            TokenKind::Mut => match self.next().unwrap_or(EOF.clone()).kind {
                TokenKind::Identifier(name) => PatternKind::Ident(BindingIdent {
                    name,
                    span,
                    mutable: true,
                }),
                _ => panic!("expected identifier after 'mut'"),
            },
            TokenKind::StrLit(value) => PatternKind::Lit(LitPat {
                lit: Literal::String(value),
            }),
            TokenKind::NumLit(value) => PatternKind::Lit(LitPat {
                lit: Literal::Number(value),
            }),
            TokenKind::BoolLit(value) => PatternKind::Lit(LitPat {
                lit: Literal::Boolean(value),
            }),
            TokenKind::Null => PatternKind::Lit(LitPat { lit: Literal::Null }),
            TokenKind::Undefined => PatternKind::Lit(LitPat {
                lit: Literal::Undefined,
            }),
            TokenKind::LeftBracket => {
                let mut elems: Vec<Option<TuplePatElem>> = vec![];
                let mut has_rest = false;
                while self.peek().unwrap_or(&EOF).kind != TokenKind::RightBracket {
                    match &self.peek().unwrap_or(&EOF).kind {
                        TokenKind::DotDotDot => {
                            if has_rest {
                                panic!("only one rest pattern is allowed per object pattern");
                            }
                            elems.push(Some(TuplePatElem {
                                pattern: self.parse_pattern()?,
                                init: None,
                            }));
                            has_rest = true;
                        }
                        _ => {
                            elems.push(Some(TuplePatElem {
                                pattern: self.parse_pattern()?,
                                init: None,
                            }));
                        }
                    }

                    // TODO: don't allow commas after rest pattern
                    if self.peek().unwrap_or(&EOF).kind == TokenKind::Comma {
                        self.next();
                    } else {
                        break;
                    }
                }

                span = merge_spans(&span, &self.peek().unwrap_or(&EOF).span);
                assert_eq!(
                    self.next().unwrap_or(EOF.clone()).kind,
                    TokenKind::RightBracket
                );

                PatternKind::Tuple(TuplePat {
                    elems,
                    optional: false,
                })
            }
            TokenKind::LeftBrace => {
                let mut props: Vec<ObjectPatProp> = vec![];

                while self
                    .peek_with_mode(IdentMode::PropName)
                    .unwrap_or(&EOF)
                    .kind
                    != TokenKind::RightBrace
                {
                    let first = self.peek_with_mode(IdentMode::PropName).unwrap_or(&EOF);
                    let first_span = first.span;
                    match &self.next().unwrap_or(EOF.clone()).kind {
                        TokenKind::Identifier(name) => {
                            if self.peek().unwrap_or(&EOF).kind == TokenKind::Colon {
                                self.next();

                                let pattern = self.parse_pattern()?;

                                // TODO: handle `var` and `mut` modifiers
                                props.push(ObjectPatProp::KeyValue(KeyValuePatProp {
                                    span: merge_spans(&first_span, &pattern.span),
                                    key: Ident {
                                        name: name.clone(),
                                        span: first_span,
                                    },
                                    value: Box::new(pattern),
                                    init: None,
                                }));
                            } else {
                                // TODO: handle `var` and `mut` modifiers
                                props.push(ObjectPatProp::Shorthand(ShorthandPatProp {
                                    span: first_span,
                                    ident: BindingIdent {
                                        name: name.clone(),
                                        span: first_span,
                                        mutable: false,
                                    },
                                    init: None,
                                }))
                            }

                            // require a comma or right brace
                            match self.peek().unwrap_or(&EOF).kind {
                                TokenKind::Comma => {
                                    self.next();
                                    continue;
                                }
                                TokenKind::RightBrace => {
                                    break;
                                }
                                _ => panic!("expected comma or right brace"),
                            }
                        }
                        TokenKind::DotDotDot => {
                            props.push(ObjectPatProp::Rest(RestPat {
                                arg: Box::new(self.parse_pattern()?),
                            }));

                            match self.peek().unwrap_or(&EOF).kind {
                                TokenKind::Comma => {
                                    self.next();
                                    continue;
                                }
                                TokenKind::RightBrace => {
                                    break;
                                }
                                _ => panic!("expected comma or right brace"),
                            }
                        }
                        TokenKind::Mut => match &self.next().unwrap_or(EOF.clone()).kind {
                            TokenKind::Identifier(name) => {
                                props.push(ObjectPatProp::Shorthand(ShorthandPatProp {
                                    span: first_span,
                                    ident: BindingIdent {
                                        name: name.clone(),
                                        span: first_span,
                                        mutable: true,
                                    },
                                    init: None,
                                }))
                            }
                            _ => panic!("expected identifier after 'mut'"),
                        },
                        _ => panic!("expected identifier or rest pattern"),
                    }
                }

                span = merge_spans(&span, &self.peek().unwrap_or(&EOF).span);
                assert_eq!(
                    self.next().unwrap_or(EOF.clone()).kind,
                    TokenKind::RightBrace
                );

                PatternKind::Object(ObjectPat {
                    props,
                    optional: false,
                })
            }
            // This code can be called when parsing rest patterns in function params.
            TokenKind::DotDotDot => PatternKind::Rest(RestPat {
                arg: Box::new(self.parse_pattern()?),
            }),
            TokenKind::Underscore => PatternKind::Wildcard,
            token => {
                panic!("expected token to start type annotation, found {:?}", token)
            }
        };

        Ok(Pattern {
            span,
            kind,
            inferred_type: None,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::parser::Parser;

    pub fn parse(input: &str) -> Pattern {
        let mut parser = Parser::new(input);
        parser.parse_pattern().unwrap()
    }

    #[test]
    fn parse_literal_patterns() {
        insta::assert_debug_snapshot!(parse("123"));
        insta::assert_debug_snapshot!(parse("true"));
        insta::assert_debug_snapshot!(parse("false"));
        insta::assert_debug_snapshot!(parse("null"));
        insta::assert_debug_snapshot!(parse("undefined"));
        insta::assert_debug_snapshot!(parse(r#""hello""#));
    }

    #[test]
    fn parse_tuple_patterns() {
        insta::assert_debug_snapshot!(parse("[a, b, mut c]"));
        insta::assert_debug_snapshot!(parse("[a, b, ...c]"));
    }

    #[test]
    #[should_panic]
    fn parse_tuple_patterns_multiple_rest() {
        insta::assert_debug_snapshot!(parse("[...a, ...b, ...c]"));
    }

    #[test]
    fn parse_object_patterns() {
        insta::assert_debug_snapshot!(parse("{x, y, mut z}"));
        insta::assert_debug_snapshot!(parse("{x, y, ...z}"));
        insta::assert_debug_snapshot!(parse("{x: a, y: b, z: mut c}"));
        insta::assert_debug_snapshot!(parse("{x: {y: {z}}}"));
    }

    #[test]
    fn parse_object_patterns_multiple_rest() {
        insta::assert_debug_snapshot!(parse("{...x, ...y, ...z}"));
    }

    #[test]
    fn parse_wildcard() {
        insta::assert_debug_snapshot!(parse("_"));
    }

    #[test]
    fn parse_rest() {
        insta::assert_debug_snapshot!(parse("...rest"));
    }

    #[test]
    fn parse_mixed_patterns() {
        insta::assert_debug_snapshot!(parse(r#"{kind: "foo", bar: _, values: [head, ...tail]}"#));
    }
}
