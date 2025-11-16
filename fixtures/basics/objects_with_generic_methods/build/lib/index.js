export const container = {value: 5, getValue(temp1) {
  const defaultValue = temp1;
  let temp2;
  if (this.value != 0) {
    return this.value;
  } else {
    return defaultValue;
  }
  temp2;
}, addNums1(temp3, temp4) {
  const a = temp3;
  const b = temp4;
  return a + b;
}, addStrs1(temp5, temp6) {
  const a = temp5;
  const b = temp6;
  return this.addNums1(a, b);
}, addNums2(temp7, temp8) {
  const a = temp7;
  const b = temp8;
  return this.addNums1(a, b);
}};
export const a = container.getValue("default");
export const b = container.getValue(10);
export const c = container.addNums2("hello", 5);
//# sourceMappingURL=./index.js.map
