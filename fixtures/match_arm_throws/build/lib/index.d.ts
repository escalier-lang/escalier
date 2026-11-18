export declare namespace Expr {
  export type Num = {};
  export const Num: {new (value: number): Expr, [Symbol.customMatcher](subject: Num): [number]};
  export type Neg = {};
  export const Neg: {new (operand: Expr): Expr, [Symbol.customMatcher](subject: Neg): [Expr]};
  export type Div = {};
  export const Div: {new (left: Expr, right: Expr): Expr, [Symbol.customMatcher](subject: Div): [Expr, Expr]};
}
export declare type Expr = Expr.Num | Expr.Neg | Expr.Div;
export declare function checked(n: number): number;
export declare function evaluate(expr: Expr): number;
export declare function sign(n: number, strict: boolean): number;
