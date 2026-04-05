export function add(temp1, temp2) {
  const a = temp1;
  const b = temp2;
  return a + b;
}
export function fst(temp3, temp4) {
  const a = temp3;
  const b = temp4;
  return a;
}
export const first = fst(1, "a");
export function fstNum(temp5, temp6) {
  const a = temp5;
  const b = temp6;
  return a;
}
export function fstNumWrapper(temp7, temp8) {
  const a = temp7;
  const b = temp8;
  return fstNum(a, b);
}
export function identity(temp9) {
  const x = temp9;
  return x;
}
export function snd(temp10, temp11) {
  const a = temp10;
  const b = temp11;
  return b;
}
export const second = snd(1, "a");
export const sum = add(1, 2);
export const w = fstNumWrapper(1, 2);
export const x = identity(5);
export const y = identity("hello");
//# sourceMappingURL=./index.js.map
