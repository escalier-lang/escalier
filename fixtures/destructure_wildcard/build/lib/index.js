export function afterOne(temp1) {
  const [temp2, b] = temp1;
  if (temp2 != 1) {
    throw new TypeError("The value does not match the literal in the destructuring pattern");
  }
  return b;
}
export const [temp3, other] = [1, 2];
if (temp3 != 1) {
  throw new TypeError("The value does not match the literal in the destructuring pattern");
}
export const fromLiteral = other;
export const [temp4, second] = [1, 2];
export const fromWildcard = second;
export function secondOf(temp5) {
  const [temp6, b] = temp5;
  return b;
}
export const secondOfClosure = function (temp7) {
  const [temp8, b] = temp7;
  return b;
};
export function sumSeconds(temp9) {
  const pairs = temp9;
  let sum = 0;
  for (const temp10 of pairs) {
    const [temp11, b] = temp10;
    sum = sum + b;
  }
  return sum;
}
//# sourceMappingURL=./index.js.map
