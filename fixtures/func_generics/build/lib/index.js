export const a = 5;
export const b = "hello";
export const fst = function (temp1, temp2) {
  const a = temp1;
  const b = temp2;
  return a;
};
export function identity(temp3) {
  const value = temp3;
  return value;
}
export const x = identity(a);
export const y = identity(b);
export const z = fst(a, a);
//# sourceMappingURL=./index.js.map
