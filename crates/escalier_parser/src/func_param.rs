use escalier_ast::*;

use crate::parse_error::ParseError;
use crate::parser::*;
use crate::token::*;

impl<'a> Parser<'a> {
    pub fn parse_params(&mut self) -> Result<Vec<FuncParam>, ParseError> {
        assert_eq!(
            self.next().unwrap_or(EOF.clone()).kind,
            TokenKind::LeftParen
        );

        let mut params: Vec<FuncParam> = Vec::new();
        while self.peek().unwrap_or(&EOF).kind != TokenKind::RightParen {
            let pattern = self.parse_pattern()?;

            let optional = if let TokenKind::Question = self.peek().unwrap_or(&EOF).kind {
                self.next().unwrap_or(EOF.clone());
                true
            } else {
                false
            };

            if let TokenKind::Colon = self.peek().unwrap_or(&EOF).kind {
                self.next().unwrap_or(EOF.clone());
                params.push(FuncParam {
                    pattern,
                    type_ann: Some(self.parse_type_ann()?),
                    optional,
                });
            } else {
                params.push(FuncParam {
                    pattern,
                    type_ann: None,
                    optional: false, // Should `?` be supported when there's not type param?
                });
            }

            // TODO: param defaults

            match self.peek().unwrap_or(&EOF).kind {
                TokenKind::RightParen => break,
                TokenKind::Comma => {
                    self.next().unwrap_or(EOF.clone());
                }
                _ => panic!(
                    "Expected comma or right paren, got {:?}",
                    self.peek().unwrap_or(&EOF)
                ),
            }
        }

        assert_eq!(
            self.next().unwrap_or(EOF.clone()).kind,
            TokenKind::RightParen
        );

        Ok(params)
    }

    pub fn parse_method_params(&mut self) -> Result<(Vec<FuncParam>, bool), ParseError> {
        assert_eq!(
            self.next().unwrap_or(EOF.clone()).kind,
            TokenKind::LeftParen
        );

        let mutates = if let TokenKind::Mut = self.peek().unwrap_or(&EOF).kind {
            self.next(); // consume 'mut'
            true
        } else {
            false
        };

        assert_eq!(
            self.next().unwrap_or(EOF.clone()).kind,
            TokenKind::Identifier("self".to_string())
        );

        if self.peek().unwrap_or(&EOF).kind == TokenKind::Comma {
            self.next(); // consume ','
        }

        let mut params: Vec<FuncParam> = Vec::new();
        while self.peek().unwrap_or(&EOF).kind != TokenKind::RightParen {
            let pattern = self.parse_pattern()?;

            let optional = if let TokenKind::Question = self.peek().unwrap_or(&EOF).kind {
                self.next().unwrap_or(EOF.clone());
                true
            } else {
                false
            };

            if let TokenKind::Colon = self.peek().unwrap_or(&EOF).kind {
                self.next().unwrap_or(EOF.clone());
                params.push(FuncParam {
                    pattern,
                    type_ann: Some(self.parse_type_ann()?),
                    optional,
                });
            } else {
                params.push(FuncParam {
                    pattern,
                    type_ann: None,
                    optional: false, // Should `?` be supported when there's not type param?
                });
            }

            // TODO: param defaults

            match self.peek().unwrap_or(&EOF).kind {
                TokenKind::RightParen => break,
                TokenKind::Comma => {
                    self.next().unwrap_or(EOF.clone());
                }
                _ => panic!(
                    "Expected comma or right paren, got {:?}",
                    self.peek().unwrap_or(&EOF)
                ),
            }
        }

        assert_eq!(
            self.next().unwrap_or(EOF.clone()).kind,
            TokenKind::RightParen
        );

        Ok((params, mutates))
    }
}
