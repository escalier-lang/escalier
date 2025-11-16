export function addNums1(temp1, temp2) {
  const a = temp1;
  const b = temp2;
  return a + b;
}
export function addStrs1(temp3, temp4) {
  const a = temp3;
  const b = temp4;
  return addNums1(a, b);
}
export function addNums2(temp5, temp6) {
  const a = temp5;
  const b = temp6;
  return addNums1(a, b);
}
export const addStrs3 = function (temp7, temp8) {
  const a = temp7;
  const b = temp8;
  return addNums1(a, b);
};
export const addNums4 = function (temp9, temp10) {
  const a = temp9;
  const b = temp10;
  return addNums1(a, b);
};
export function main() {
  addNums2("hello", 5);
  addNums4("hello", 5);
}
//# sourceMappingURL=./index.js.map
