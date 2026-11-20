export function Va(temp1) {
  const x = temp1;
  return undefined;
}
export function Va2(...temp2) {
  const xs = temp2;
  return undefined;
}
export function a(temp3) {
  const f = temp3;
  return undefined;
}
export function aHelper(temp4) {
  const f = temp4;
  return a(f);
}
export const withVa = aHelper(Va);
export const withVa2 = aHelper(Va2);
//# sourceMappingURL=./index.js.map
