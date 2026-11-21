export function afterOne(temp1) {
  const [, b] = temp1;
  if (temp1[0] != 1) {
    throw new TypeError("The value does not match the literal in the destructuring pattern");
  }
  return b;
}
const temp2 = [1, 2];
export const [, other] = temp2;
if (temp2[0] != 1) {
  throw new TypeError("The value does not match the literal in the destructuring pattern");
}
export const fromLiteral = other;
export const [, second] = [1, 2];
export const fromWildcard = second;
export function othersOf(temp3) {
  const {x: temp4, ...others} = temp3;
  return others;
}
export function secondOf(temp5) {
  const [, b] = temp5;
  return b;
}
export const secondOfClosure = function (temp6) {
  const [, b] = temp6;
  return b;
};
export function sumSeconds(temp7) {
  const pairs = temp7;
  let sum = 0;
  for (const temp8 of pairs) {
    const [, b] = temp8;
    sum = sum + b;
  }
  return sum;
}
export function yOf(temp9) {
  const {y} = temp9;
  return y;
}
export function yWhenXIsOne(temp10) {
  const {y} = temp10;
  if (temp10.x != 1) {
    throw new TypeError("The value does not match the literal in the destructuring pattern");
  }
  return y;
}
//# sourceMappingURL=./index.js.map
