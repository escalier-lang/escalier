import { InvokeCustomMatcherOrThrow } from "@escalier/runtime";
export const Expr = {};
class Expr__Num {
  constructor(temp1) {
    const value = temp1;
    this.value = value;
  }
  static [Symbol.customMatcher](subject) {
    return [subject.value];
  }
}
Expr.Num = Expr__Num;
class Expr__Neg {
  constructor(temp2) {
    const operand = temp2;
    this.operand = operand;
  }
  static [Symbol.customMatcher](subject) {
    return [subject.operand];
  }
}
Expr.Neg = Expr__Neg;
class Expr__Div {
  constructor(temp3, temp4) {
    const left = temp3;
    const right = temp4;
    this.left = left;
    this.right = right;
  }
  static [Symbol.customMatcher](subject) {
    return [subject.left, subject.right];
  }
}
Expr.Div = Expr__Div;
export function checked(temp5) {
  const n = temp5;
  let temp6;
  let temp7;
  temp7 = n;
  if (temp7 == 0) {
    throw "zero";
  } else {
    temp6 = n;
  }
  return temp6;
}
export function evaluate(temp8) {
  const expr = temp8;
  let temp9;
  let temp10;
  temp10 = expr;
  if (temp10 instanceof Expr.Num) {
    const [temp15] = InvokeCustomMatcherOrThrow(Expr.Num, temp10, undefined);
    const value = temp15;
    temp9 = value;
  } else if (temp10 instanceof Expr.Neg) {
    const [temp14] = InvokeCustomMatcherOrThrow(Expr.Neg, temp10, undefined);
    const operand = temp14;
    temp9 = -evaluate(operand);
  } else if (temp10 instanceof Expr.Div) {
    const [temp11, temp12] = InvokeCustomMatcherOrThrow(Expr.Div, temp10, undefined);
    const left = temp11;
    const right = temp12;
    let temp13;
    if (evaluate(right) == 0) {
      throw "division by zero";
    } else {
      temp13 = evaluate(left) / evaluate(right);
    }
    temp9 = temp13;
  }
  return temp9;
}
export function sign(temp16, temp17) {
  const n = temp16;
  const strict = temp17;
  let temp18;
  let temp19;
  temp19 = n;
  if (temp19 == 0) {
    if (strict) {
      throw "zero";
    } else {
      throw "no sign";
    }
  } else {
    let temp20;
    if (n > 0) {
      temp20 = 1;
    } else {
      temp20 = -1;
    }
    temp18 = temp20;
  }
  return temp18;
}
//# sourceMappingURL=./index.js.map
