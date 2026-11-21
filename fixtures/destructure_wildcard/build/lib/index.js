export const [temp1, other] = [1, 2];
export const fromLiteral = other;
export const [temp2, second] = [1, 2];
export const fromWildcard = second;
export function secondOf(temp3) {
  const [temp4, b] = temp3;
  return b;
}
export const secondOfClosure = function (temp5) {
  const [temp6, b] = temp5;
  return b;
};
export function sumSeconds(temp7) {
  const pairs = temp7;
  let sum = 0;
  for (const temp8 of pairs) {
    const [temp9, b] = temp8;
    sum = sum + b;
  }
  return sum;
}
//# sourceMappingURL=./index.js.map
