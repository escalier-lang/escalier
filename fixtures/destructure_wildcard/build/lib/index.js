export function afterNull(temp1) {
  const [, b] = temp1;
  if (temp1[0] !== null) {
    throw new TypeError("The value does not match the literal in the destructuring pattern");
  }
  return b;
}
export function afterOne(temp2) {
  const [, b] = temp2;
  if (temp2[0] !== 1) {
    throw new TypeError("The value does not match the literal in the destructuring pattern");
  }
  return b;
}
const temp3 = [1, 2];
export const [, other] = temp3;
if (temp3[0] !== 1) {
  throw new TypeError("The value does not match the literal in the destructuring pattern");
}
export const fromLiteral = other;
export const [, second] = [1, 2];
export const fromWildcard = second;
export function othersOf(temp4) {
  const {x: temp5, ...others} = temp4;
  return others;
}
export function secondOf(temp6) {
  const [, b] = temp6;
  return b;
}
export const secondOfClosure = function (temp7) {
  const [, b] = temp7;
  return b;
};
export function sumSeconds(temp8) {
  const pairs = temp8;
  let sum = 0;
  for (const temp9 of pairs) {
    const [, b] = temp9;
    sum = sum + b;
  }
  return sum;
}
export function yOf(temp10) {
  const {y} = temp10;
  return y;
}
export function yWhenXIsOne(temp11) {
  const {y} = temp11;
  if (temp11.x !== 1) {
    throw new TypeError("The value does not match the literal in the destructuring pattern");
  }
  return y;
}
//# sourceMappingURL=./index.js.map
